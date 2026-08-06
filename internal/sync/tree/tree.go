// Package tree is the filesystem side of sync: scanning a root into entries,
// staging content beside it, and applying changes by atomic rename.
//
// Both ends use it. The endpoint runs it inside the sandbox; the controller
// runs it against the local root.
package tree

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
)

const (
	// StateDirName holds everything sync keeps beside a root it is staging
	// into: the marker and the staging directory. One dotted directory, and it
	// excludes itself from every scan.
	StateDirName = ".agyn-sync"

	markerFileName  = "id"
	stagingDirName  = "staging"
	stagingTempPerm = 0o700

	// filePerm and dirPerm are what created entries get. Ownership is never
	// propagated — a local uid is meaningless in the container — so entries are
	// created as whatever user the process runs as.
	filePerm     fs.FileMode = 0o644
	filePermExec fs.FileMode = 0o755
	dirPerm      fs.FileMode = 0o755
)

// Snapshot is one side's view of a root.
type Snapshot struct {
	Entries  []*syncv1.Entry
	Problems []*syncv1.Problem
	Rehashed uint64
}

// StateDir is the sync state directory for a root.
func StateDir(root string) string { return filepath.Join(root, StateDirName) }

// StagingDir is where content lands before being renamed into place. It sits
// under the root so the final rename is same-filesystem and therefore atomic.
func StagingDir(root string) string { return filepath.Join(StateDir(root), stagingDirName) }

// Scan walks root and returns an entry per path. Per-path failures become
// problems rather than errors: one unreadable file must not stop the rest of
// the tree from syncing.
//
// cache maps a relative path to a previously known (size, mtime, digest). An
// entry whose size and mtime are unchanged keeps its digest without being read.
func Scan(root string, cache map[string]*syncv1.CachedDigest) (*Snapshot, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	snapshot := &Snapshot{}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			snapshot.problem(relative(root, path), err)
			// A directory we cannot read is skipped, not fatal.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel := relative(root, path)
		if rel == "" {
			return nil
		}
		if rel == StateDirName {
			return fs.SkipDir
		}
		entry, problem := entryFor(path, rel, d, cache, snapshot)
		if problem != nil {
			snapshot.Problems = append(snapshot.Problems, problem)
			return nil
		}
		snapshot.Entries = append(snapshot.Entries, entry)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(snapshot.Entries, func(i, j int) bool { return snapshot.Entries[i].Path < snapshot.Entries[j].Path })
	sort.Slice(snapshot.Problems, func(i, j int) bool { return snapshot.Problems[i].Path < snapshot.Problems[j].Path })
	return snapshot, nil
}

func entryFor(path, rel string, d fs.DirEntry, cache map[string]*syncv1.CachedDigest, snapshot *Snapshot) (*syncv1.Entry, *syncv1.Problem) {
	info, err := d.Info()
	if err != nil {
		return nil, &syncv1.Problem{Path: rel, Message: err.Error()}
	}
	entry := &syncv1.Entry{
		Path:             rel,
		ModifiedUnixNano: info.ModTime().UnixNano(),
	}
	switch {
	case d.IsDir():
		entry.Kind = syncv1.EntryKind_ENTRY_KIND_DIRECTORY
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return nil, &syncv1.Problem{Path: rel, Message: err.Error()}
		}
		entry.Kind = syncv1.EntryKind_ENTRY_KIND_SYMLINK
		entry.Target = filepath.ToSlash(target)
	case info.Mode().IsRegular():
		entry.Kind = syncv1.EntryKind_ENTRY_KIND_FILE
		entry.Size = info.Size()
		entry.Executable = info.Mode()&0o100 != 0
		if cached, ok := cache[rel]; ok && cached.GetSize() == info.Size() && cached.GetModifiedUnixNano() == info.ModTime().UnixNano() {
			entry.Digest = cached.GetDigest()
			break
		}
		digest, err := DigestFile(path)
		if err != nil {
			return nil, &syncv1.Problem{Path: rel, Message: err.Error()}
		}
		entry.Digest = digest
		snapshot.Rehashed++
	default:
		// Sockets, devices, fifos. Reported rather than guessed at.
		entry.Kind = syncv1.EntryKind_ENTRY_KIND_UNSUPPORTED
	}
	return entry, nil
}

func (s *Snapshot) problem(path string, err error) {
	s.Problems = append(s.Problems, &syncv1.Problem{Path: path, Message: err.Error()})
}

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// DigestFile returns the hex sha256 of a file's contents, streamed rather than
// buffered — the endpoint shares the container's memory limit with the shell.
func DigestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// Staging holds content written beside a root, waiting to be renamed into it.
type Staging struct {
	dir string
}

// OpenStaging creates the staging directory for a root.
func OpenStaging(root string) (*Staging, error) {
	dir := StagingDir(root)
	if err := os.MkdirAll(dir, stagingTempPerm); err != nil {
		return nil, err
	}
	return &Staging{dir: dir}, nil
}

func (s *Staging) Path(digest string) string { return filepath.Join(s.dir, digest) }

// Has reports whether the content is already staged, so it need not be sent
// again. A digest names its own content, so presence is sufficient.
func (s *Staging) Has(digest string) bool {
	_, err := os.Stat(s.Path(digest))
	return err == nil
}

// Put streams content into staging and returns the digest actually computed
// over the bytes received, which the caller compares against what was promised.
// Nothing lands at its final name until the content is whole.
func (s *Staging) Put(digest string, src io.Reader, limit int64) (string, error) {
	temp, err := os.CreateTemp(s.dir, ".incoming-*")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	defer func() {
		temp.Close()
		os.Remove(tempName)
	}()

	hasher := sha256.New()
	reader := io.Reader(src)
	if limit >= 0 {
		// One byte over the limit is enough to detect a source that grew.
		reader = io.LimitReader(src, limit+1)
	}
	written, err := io.Copy(io.MultiWriter(temp, hasher), reader)
	if err != nil {
		return "", err
	}
	if limit >= 0 && written > limit {
		return "", fmt.Errorf("content for %s exceeds the declared %d bytes", digest, limit)
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != digest {
		return actual, nil
	}
	if err := os.Rename(tempName, s.Path(digest)); err != nil {
		return "", err
	}
	return actual, nil
}

// CollectStale removes staging directories left by sessions that were
// terminated mid-transfer. Orphaned staged content is inert but occupies the
// workspace volume, so it is collected at handshake rather than left to
// accumulate.
func CollectStale(root string) (uint32, error) {
	staging := StagingDir(root)
	entries, err := os.ReadDir(staging)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var collected uint32
	for _, entry := range entries {
		// Partially received content only: a fully staged file is named for its
		// digest and is still useful to a later session.
		if strings.HasPrefix(entry.Name(), ".incoming-") {
			if err := os.Remove(filepath.Join(staging, entry.Name())); err == nil {
				collected++
			}
		}
	}
	return collected, nil
}

// Marker reads the root's session marker. An empty string with a nil error
// means the root carries none.
func Marker(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(StateDir(root), markerFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteMarker records the session a root belongs to. Only sync writes one: a cp
// establishes no relationship, so planting a marker would assert something
// untrue and change how a later sync reads the root.
func WriteMarker(root, sessionID string) error {
	if err := os.MkdirAll(StateDir(root), dirPerm); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(StateDir(root), markerFileName), []byte(sessionID+"\n"), 0o644)
}

// IsEmpty reports whether a root holds anything sync would care about. The
// state directory does not count — a root holding only sync's own bookkeeping
// is empty as far as reconciliation is concerned.
func IsEmpty(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() != StateDirName {
			return false, nil
		}
	}
	return true, nil
}

// Transition applies changes, returning one result per change. A failure on one
// path does not abort the batch: the whole point of per-path results is that a
// single unwritable file does not strand every other change in the cycle.
func Transition(root string, staging *Staging, changes []*syncv1.Change) []*syncv1.Result {
	results := make([]*syncv1.Result, 0, len(changes))
	for _, change := range changes {
		results = append(results, applyChange(root, staging, change))
	}
	return results
}

func applyChange(root string, staging *Staging, change *syncv1.Change) *syncv1.Result {
	result := &syncv1.Result{Path: change.GetPath()}
	target, err := ResolveWithin(root, change.GetPath())
	if err != nil {
		result.Message = err.Error()
		return result
	}
	switch change.GetKind() {
	case syncv1.ChangeKind_CHANGE_KIND_CREATE_DIRECTORY:
		err = os.MkdirAll(target, dirPerm)
	case syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE:
		err = createFile(staging, target, change)
	case syncv1.ChangeKind_CHANGE_KIND_CREATE_SYMLINK:
		err = createSymlink(target, change.GetTarget())
	case syncv1.ChangeKind_CHANGE_KIND_REMOVE:
		err = os.RemoveAll(target)
	case syncv1.ChangeKind_CHANGE_KIND_SET_EXECUTABLE:
		err = os.Chmod(target, permFor(change.GetExecutable()))
	default:
		err = fmt.Errorf("unsupported change kind %s", change.GetKind())
	}
	if err != nil {
		result.Message = err.Error()
		return result
	}
	result.Applied = true
	return result
}

// createFile renames staged content into place. The rename is atomic and
// same-filesystem, so the destination never holds a partial file even if the
// endpoint is terminated at this instruction.
func createFile(staging *Staging, target string, change *syncv1.Change) error {
	if staging == nil {
		return errors.New("no staging directory for this session")
	}
	staged := staging.Path(change.GetDigest())
	if _, err := os.Stat(staged); err != nil {
		return fmt.Errorf("content %s is not staged", change.GetDigest())
	}
	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return err
	}
	if err := os.Chmod(staged, permFor(change.GetExecutable())); err != nil {
		return err
	}
	return os.Rename(staged, target)
}

func createSymlink(target, linkTarget string) error {
	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return err
	}
	// A symlink cannot be replaced in place; removing first is safe because the
	// link itself carries no content.
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Symlink(filepath.FromSlash(linkTarget), target)
}

func permFor(executable bool) fs.FileMode {
	if executable {
		return filePermExec
	}
	return filePerm
}

// ResolveWithin refuses a path that would land outside the root. Paths arrive
// over a pipe from the other side, so a traversal is a real possibility and not
// merely a defensive check.
func ResolveWithin(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("path %q is absolute", rel)
	}
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the root", rel)
	}
	if cleaned == StateDirName || strings.HasPrefix(cleaned, StateDirName+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is sync's own state", rel)
	}
	return filepath.Join(root, cleaned), nil
}
