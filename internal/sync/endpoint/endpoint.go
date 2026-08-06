// Package endpoint is the in-sandbox side of workspace sync. It executes
// filesystem operations it is told to perform and computes nothing: the
// controller on the engineer's machine decides every action.
//
// That split follows from the lifecycle. This process is terminated without
// warning whenever the WebSocket drops, the sandbox idles out, or the TTL
// fires, so anything authoritative held here is state that is routinely lost.
// It persists exactly two things, both disposable: a root marker and a staging
// directory.
package endpoint

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/internal/sync/tree"
	"github.com/agynio/agyn-cli/internal/sync/wire"
)

const (
	// ProtocolVersion is what this build speaks. It is the protocol, never the
	// build: the CLI is installed by the engineer and the in-sandbox binary
	// ships with the platform, so differing builds are the normal case.
	ProtocolVersion    = 1
	MinProtocolVersion = 1
)

// Options configures one endpoint session.
type Options struct {
	// Root is the directory to serve, as given on the command line.
	Root string
	// Workspace confines Root after symlink resolution. Empty disables the
	// check, which is what a local test harness wants; inside a sandbox it is
	// the workspace mount and the confinement is real.
	Workspace string
	// Version reports the endpoint build, for diagnostics only. Never used to
	// accept or refuse a session.
	Version string
}

// Serve runs the request loop until the input stream ends. It returns nil on a
// clean end-of-stream: the controller closing the pipe is how a session ends.
func Serve(opts Options, in io.Reader, out io.Writer) error {
	session := &session{opts: opts, conn: wire.NewConn(in, out)}
	return session.run()
}

type session struct {
	opts    Options
	conn    *wire.Conn
	root    string
	staging *tree.Staging
	ready   bool
}

func (s *session) run() error {
	for {
		frame, err := s.conn.ReadFrame()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
		request := frame.GetRequest()
		if request == nil {
			return fmt.Errorf("expected a request frame, got %T", frame.GetPayload())
		}
		if err := s.dispatch(request); err != nil {
			return err
		}
	}
}

func (s *session) dispatch(request *syncv1.Request) error {
	switch payload := request.GetRequest().(type) {
	case *syncv1.Request_Handshake:
		return s.handshake(payload.Handshake)
	case *syncv1.Request_Scan:
		return s.scan(payload.Scan)
	case *syncv1.Request_StageQuery:
		return s.stageQuery(payload.StageQuery)
	case *syncv1.Request_StagePut:
		return s.stagePut(payload.StagePut)
	case *syncv1.Request_Transition:
		return s.transition(payload.Transition)
	case *syncv1.Request_Supply:
		return s.supply(payload.Supply)
	default:
		return s.fail(syncv1.ErrorCode_ERROR_CODE_INTERNAL, fmt.Sprintf("unsupported request %T", payload))
	}
}

func (s *session) fail(code syncv1.ErrorCode, message string) error {
	return s.conn.WriteResponse(&syncv1.Response{
		Response: &syncv1.Response_Error{Error: &syncv1.ErrorResponse{Code: code, Message: message}},
	})
}

func (s *session) requireReady() error {
	if !s.ready {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_INTERNAL, "handshake required first")
	}
	return nil
}

func (s *session) handshake(request *syncv1.HandshakeRequest) error {
	version, err := negotiate(request.GetVersionMin(), request.GetVersionMax())
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_PROTOCOL_VERSION, err.Error())
	}

	// A sync session establishes a relationship with a directory and may create
	// it; a copy never does, because it asserts nothing about a root it is only
	// reading through.
	root, err := s.resolveRoot(request.GetMarkerMode() == syncv1.MarkerMode_MARKER_MODE_CREATE)
	if err != nil {
		code := syncv1.ErrorCode_ERROR_CODE_ROOT_INVALID
		if errors.Is(err, errOutsideWorkspace) {
			code = syncv1.ErrorCode_ERROR_CODE_ROOT_OUTSIDE_WORKSPACE
		}
		return s.fail(code, err.Error())
	}
	s.root = root

	response := &syncv1.HandshakeResponse{
		Version:         version,
		ResolvedRoot:    root,
		RootExists:      true,
		WatchMode:       syncv1.WatchMode_WATCH_MODE_POLL,
		EndpointVersion: s.opts.Version,
	}

	empty, err := tree.IsEmpty(root)
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_ROOT_INVALID, err.Error())
	}
	response.RootEmpty = empty

	// Staging left by a session that was terminated mid-transfer is inert but
	// occupies the workspace volume, so it goes now rather than accumulating.
	collected, err := tree.CollectStale(root)
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
	}
	response.CollectedStagingDirs = collected

	marker, err := tree.Marker(root)
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
	}
	// Only sync writes a marker. cp reads one and never plants one: a copy
	// creates no relationship, and a marker would change how a later sync reads
	// this root.
	if marker == "" && request.GetMarkerMode() == syncv1.MarkerMode_MARKER_MODE_CREATE && request.GetExpectedSessionId() != "" {
		if err := tree.WriteMarker(root, request.GetExpectedSessionId()); err != nil {
			return s.fail(syncv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
		}
		marker = request.GetExpectedSessionId()
	}
	response.SessionId = marker

	staging, err := tree.OpenStaging(root)
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_NO_SPACE, err.Error())
	}
	s.staging = staging
	s.ready = true

	return s.conn.WriteResponse(&syncv1.Response{
		Response: &syncv1.Response_Handshake{Handshake: response},
	})
}

func negotiate(min, max uint32) (uint32, error) {
	if min == 0 && max == 0 {
		return 0, fmt.Errorf("controller sent no protocol version range")
	}
	if min > max {
		return 0, fmt.Errorf("controller sent an inverted version range %d..%d", min, max)
	}
	if max < MinProtocolVersion || min > ProtocolVersion {
		return 0, fmt.Errorf("controller speaks %d..%d, endpoint speaks %d..%d", min, max, MinProtocolVersion, ProtocolVersion)
	}
	selected := max
	if selected > ProtocolVersion {
		selected = ProtocolVersion
	}
	return selected, nil
}

var errOutsideWorkspace = errors.New("root resolves outside the workspace mount")

// createRoot makes a missing sync root, but only after confining the nearest
// existing ancestor: a path that does not exist yet cannot be resolved, and
// creating it first would let a root outside the workspace be brought into
// being by the very check meant to refuse it.
func (s *session) createRoot(root string) error {
	if _, err := os.Stat(root); err == nil {
		return nil
	}
	ancestor := root
	for {
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("no existing ancestor of %q", root)
		}
		ancestor = parent
		if _, err := os.Stat(ancestor); err == nil {
			break
		}
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return err
	}
	if err := s.confine(resolvedAncestor); err != nil {
		return err
	}
	return os.MkdirAll(root, 0o755)
}

// confine refuses a path that lands outside the workspace mount.
func (s *session) confine(resolved string) error {
	workspace := strings.TrimSpace(s.opts.Workspace)
	if workspace == "" {
		return nil
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace %q: %w", workspace, err)
	}
	if resolved != resolvedWorkspace && !strings.HasPrefix(resolved, resolvedWorkspace+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s is not under %s", errOutsideWorkspace, resolved, resolvedWorkspace)
	}
	return nil
}

// resolveRoot follows symlinks and confines the result. This is the only place
// either check can happen: the Gateway validates the path lexically at issuance
// but has no mount data for a container and cannot see its filesystem.
func (s *session) resolveRoot(create bool) (string, error) {
	root := strings.TrimSpace(s.opts.Root)
	if root == "" {
		return "", errors.New("root is required")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("root %q is not absolute", root)
	}
	if create {
		if err := s.createRoot(root); err != nil {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %q: %w", root, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", resolved)
	}
	if err := s.confine(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func (s *session) scan(request *syncv1.ScanRequest) error {
	if err := s.requireReady(); err != nil {
		return err
	}
	cache := make(map[string]*syncv1.CachedDigest, len(request.GetCache()))
	for _, cached := range request.GetCache() {
		cache[cached.GetPath()] = cached
	}
	snapshot, err := tree.Scan(s.root, cache)
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_ROOT_INVALID, err.Error())
	}
	return s.conn.WriteResponse(&syncv1.Response{
		Response: &syncv1.Response_Scan{Scan: &syncv1.ScanResponse{
			Entries:  snapshot.Entries,
			Problems: snapshot.Problems,
			Rehashed: snapshot.Rehashed,
		}},
	})
}

func (s *session) stageQuery(request *syncv1.StageQueryRequest) error {
	if err := s.requireReady(); err != nil {
		return err
	}
	missing := make([]string, 0, len(request.GetDigests()))
	for _, digest := range request.GetDigests() {
		if !s.staging.Has(digest) {
			missing = append(missing, digest)
		}
	}
	available, err := availableBytes(tree.StagingDir(s.root))
	if err != nil {
		// Not knowing the free space is not a reason to refuse the session; the
		// write itself still reports ENOSPC.
		available = -1
	}
	return s.conn.WriteResponse(&syncv1.Response{
		Response: &syncv1.Response_StageQuery{StageQuery: &syncv1.StageQueryResponse{
			Missing:        missing,
			AvailableBytes: available,
		}},
	})
}

// stagePut receives one body. The body must be drained even when the request is
// rejected: the stream is a single pipe with no way to cancel, so leaving
// chunks unread would desynchronize everything after it.
func (s *session) stagePut(request *syncv1.StagePutRequest) error {
	if !s.ready {
		_ = s.conn.DiscardBody()
		return s.fail(syncv1.ErrorCode_ERROR_CODE_INTERNAL, "handshake required first")
	}
	reader, writer := io.Pipe()
	done := make(chan struct {
		digest string
		err    error
	}, 1)
	go func() {
		digest, err := s.staging.Put(request.GetDigest(), reader, request.GetSize())
		// Draining the rest keeps the pipe consistent when Put stops early.
		_, _ = io.Copy(io.Discard, reader)
		reader.Close()
		done <- struct {
			digest string
			err    error
		}{digest, err}
	}()
	bodyErr := s.conn.ReadBody(writer, -1)
	writer.Close()
	outcome := <-done

	if bodyErr != nil {
		return bodyErr
	}
	if outcome.err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_NO_SPACE, outcome.err.Error())
	}
	return s.conn.WriteResponse(&syncv1.Response{
		Response: &syncv1.Response_StagePut{StagePut: &syncv1.StagePutResponse{Digest: outcome.digest}},
	})
}

func (s *session) transition(request *syncv1.TransitionRequest) error {
	if err := s.requireReady(); err != nil {
		return err
	}
	results := tree.Transition(s.root, s.staging, request.GetChanges())
	return s.conn.WriteResponse(&syncv1.Response{
		Response: &syncv1.Response_Transition{Transition: &syncv1.TransitionResponse{Results: results}},
	})
}

func (s *session) supply(request *syncv1.SupplyRequest) error {
	if err := s.requireReady(); err != nil {
		return err
	}
	target, err := tree.ResolveWithin(s.root, request.GetPath())
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_ROOT_INVALID, err.Error())
	}
	info, err := os.Stat(target)
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_ROOT_INVALID, err.Error())
	}
	digest, err := tree.DigestFile(target)
	if err != nil {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_INTERNAL, err.Error())
	}
	// The controller named what it expects. Sending different content under
	// that name would be worse than refusing: it would be applied as if it were
	// what was asked for.
	if expected := request.GetExpectedDigest(); expected != "" && expected != digest {
		return s.fail(syncv1.ErrorCode_ERROR_CODE_ROOT_INVALID,
			fmt.Sprintf("%s changed under the scan: expected %s, found %s", request.GetPath(), expected, digest))
	}

	if err := s.conn.WriteResponse(&syncv1.Response{
		Response: &syncv1.Response_Supply{Supply: &syncv1.SupplyResponse{Size: info.Size(), Digest: digest}},
	}); err != nil {
		return err
	}
	file, err := os.Open(target)
	if err != nil {
		return err
	}
	defer file.Close()
	return s.conn.WriteBody(file, -1)
}
