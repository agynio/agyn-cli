package endpoint_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/internal/sync/client"
	"github.com/agynio/agyn-cli/internal/sync/endpoint"
	"github.com/agynio/agyn-cli/internal/sync/tree"
)

// dial runs an endpoint over in-process pipes and returns a client speaking to
// it, the way the exec stream will once the transport lands.
func dial(t *testing.T, opts endpoint.Options) *client.Client {
	t.Helper()
	toEndpoint, fromClient := io.Pipe()
	toClient, fromEndpoint := io.Pipe()

	served := make(chan error, 1)
	go func() { served <- endpoint.Serve(opts, toEndpoint, fromEndpoint) }()

	t.Cleanup(func() {
		fromClient.Close()
		if err := <-served; err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("endpoint: %v", err)
		}
		toClient.Close()
	})
	return client.New(toClient, fromClient)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRoundTripPushesAndPullsContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), "already here", 0o644)
	writeFile(t, filepath.Join(root, "nested", "tool.sh"), "#!/bin/sh\n", 0o755)

	c := dial(t, endpoint.Options{Root: root, Version: "test"})

	handshake, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, "")
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if handshake.GetVersion() != endpoint.ProtocolVersion {
		t.Fatalf("negotiated version %d, want %d", handshake.GetVersion(), endpoint.ProtocolVersion)
	}
	if handshake.GetRootEmpty() {
		t.Fatal("root holds files but was reported empty")
	}
	// cp must never plant a marker: a copy creates no relationship to identify.
	if handshake.GetSessionId() != "" {
		t.Fatalf("read-mode handshake planted a marker %q", handshake.GetSessionId())
	}
	if _, err := os.Stat(filepath.Join(tree.StateDir(root), "id")); err == nil {
		t.Fatal("read-mode handshake wrote a marker file")
	}

	scan, err := c.Scan(nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	found := map[string]*syncv1.Entry{}
	for _, entry := range scan.GetEntries() {
		found[entry.GetPath()] = entry
	}
	if _, ok := found["existing.txt"]; !ok {
		t.Fatalf("scan missed existing.txt: %v", found)
	}
	if exe := found["nested/tool.sh"]; exe == nil || !exe.GetExecutable() {
		t.Fatalf("scan lost the executable bit: %+v", exe)
	}
	// Sync's own state directory must never appear in a scan.
	for path := range found {
		if strings.HasPrefix(path, tree.StateDirName) {
			t.Fatalf("scan surfaced sync state: %s", path)
		}
	}

	// Push a file the endpoint does not have.
	body := []byte("pushed from the controller\n")
	digest := digestOf(t, body)
	query, err := c.StageQuery([]string{digest})
	if err != nil {
		t.Fatalf("stage query: %v", err)
	}
	if len(query.GetMissing()) != 1 || query.GetMissing()[0] != digest {
		t.Fatalf("expected the digest to be missing, got %v", query.GetMissing())
	}
	if err := c.StagePut(digest, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage put: %v", err)
	}
	// Staged content is not visible at its destination until the transition.
	if _, err := os.Stat(filepath.Join(root, "pushed.txt")); err == nil {
		t.Fatal("staged content appeared before the transition")
	}

	results, err := c.Transition([]*syncv1.Change{{
		Kind:   syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE,
		Path:   "pushed.txt",
		Digest: digest,
	}})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if len(results) != 1 || !results[0].GetApplied() {
		t.Fatalf("transition did not apply: %+v", results)
	}
	got, err := os.ReadFile(filepath.Join(root, "pushed.txt"))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("pushed content wrong: %q %v", got, err)
	}

	// Already-staged content is not requested a second time.
	requery, err := c.StageQuery([]string{digestOf(t, []byte("still staged"))})
	if err != nil {
		t.Fatalf("stage requery: %v", err)
	}
	if len(requery.GetMissing()) != 1 {
		t.Fatalf("expected one missing digest, got %v", requery.GetMissing())
	}

	// Pull a file back out.
	var pulled bytes.Buffer
	supply, err := c.Supply("existing.txt", found["existing.txt"].GetDigest(), &pulled)
	if err != nil {
		t.Fatalf("supply: %v", err)
	}
	if pulled.String() != "already here" {
		t.Fatalf("pulled %q", pulled.String())
	}
	if supply.GetDigest() != found["existing.txt"].GetDigest() {
		t.Fatal("supply digest disagreed with the scan")
	}
}

// A digest names its content, so a mismatch means the file moved under the scan
// and applying it would write something other than what was reconciled.
func TestSupplyRefusesContentThatChangedUnderTheScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "moving.txt"), "original", 0o644)

	c := dial(t, endpoint.Options{Root: root})
	if _, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, ""); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	var out bytes.Buffer
	_, err := c.Supply("moving.txt", digestOf(t, []byte("what the scan saw")), &out)
	if err == nil {
		t.Fatal("expected the endpoint to refuse a digest mismatch")
	}
	var endpointErr *client.Error
	if !errors.As(err, &endpointErr) {
		t.Fatalf("expected an endpoint error, got %T: %v", err, err)
	}
}

// Paths arrive over a pipe from the other side, so traversal is a real
// possibility rather than a defensive check.
func TestTransitionRefusesPathsOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim.txt")
	writeFile(t, outside, "untouched", 0o644)

	c := dial(t, endpoint.Options{Root: root})
	if _, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, ""); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	for _, path := range []string{"../victim.txt", "../../etc/passwd", tree.StateDirName + "/id"} {
		results, err := c.Transition([]*syncv1.Change{{
			Kind: syncv1.ChangeKind_CHANGE_KIND_REMOVE,
			Path: path,
		}})
		if err != nil {
			t.Fatalf("transition %s: %v", path, err)
		}
		if len(results) != 1 || results[0].GetApplied() {
			t.Fatalf("path %s was applied: %+v", path, results)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a refused traversal still touched the file outside the root: %v", err)
	}
}

// The Gateway validates the root lexically, but only a process inside the
// container can follow symlinks or see where the workspace is mounted.
func TestHandshakeConfinesTheRootToTheWorkspace(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(filepath.Join(workspace, "project"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A symlink inside the workspace pointing out of it is exactly what a
	// lexical check at the Gateway cannot catch.
	escape := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	t.Run("inside is accepted", func(t *testing.T) {
		c := dial(t, endpoint.Options{Root: filepath.Join(workspace, "project"), Workspace: workspace})
		if _, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, ""); err != nil {
			t.Fatalf("handshake: %v", err)
		}
	})

	t.Run("symlink out is refused", func(t *testing.T) {
		c := dial(t, endpoint.Options{Root: escape, Workspace: workspace})
		_, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, "")
		if err == nil {
			t.Fatal("expected a root escaping the workspace to be refused")
		}
		var endpointErr *client.Error
		if !errors.As(err, &endpointErr) {
			t.Fatalf("expected an endpoint error, got %T", err)
		}
		if endpointErr.Code != syncv1.ErrorCode_ERROR_CODE_ROOT_OUTSIDE_WORKSPACE {
			t.Fatalf("expected ROOT_OUTSIDE_WORKSPACE, got %s: %s", endpointErr.Code, endpointErr.Message)
		}
	})
}

// Sync writes a marker; cp never does. The mode is what distinguishes them.
func TestCreateModePlantsTheMarker(t *testing.T) {
	root := t.TempDir()
	c := dial(t, endpoint.Options{Root: root})

	handshake, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_CREATE, "session-abc")
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if handshake.GetSessionId() != "session-abc" {
		t.Fatalf("marker not established, got %q", handshake.GetSessionId())
	}
	if !handshake.GetRootEmpty() {
		t.Fatal("a root holding only sync state should read as empty")
	}
	marker, err := tree.Marker(root)
	if err != nil || marker != "session-abc" {
		t.Fatalf("marker on disk is %q (%v)", marker, err)
	}
}

func digestOf(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "digest-input")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	digest, err := tree.DigestFile(path)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return digest
}
