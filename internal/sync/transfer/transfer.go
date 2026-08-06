// Package transfer moves a subtree in one direction between a local root and an
// endpoint root. It is what `agyn sandbox cp` drives.
//
// A copy is not a relationship: there is no ancestor, no watching, and no
// conflict handling, because there are no two sides to keep in agreement over
// time. What it shares with sync is the staged, atomic write — content lands
// whole or not at all.
package transfer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/internal/sync/client"
	"github.com/agynio/agyn-cli/internal/sync/tree"
)

// Report is what a copy did, for the caller to print.
type Report struct {
	Files       int
	Directories int
	Bytes       int64
	// Skipped names entries a copy cannot carry — sockets, devices — reported
	// rather than silently dropped.
	Skipped []string
}

// Push copies localPath into the endpoint's root at remoteRel. recursive must
// be set for a directory, matching cp.
func Push(c *client.Client, localPath, remoteRel string, recursive bool) (*Report, error) {
	info, err := os.Lstat(localPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() && !recursive {
		return nil, fmt.Errorf("%s is a directory (use -r)", localPath)
	}

	report := &Report{}
	var changes []*syncv1.Change
	// digest -> local path, so content shared by several files is sent once.
	bodies := map[string]string{}

	if info.IsDir() {
		snapshot, err := tree.Scan(localPath, nil)
		if err != nil {
			return nil, err
		}
		changes = append(changes, &syncv1.Change{
			Kind: syncv1.ChangeKind_CHANGE_KIND_CREATE_DIRECTORY,
			Path: remoteRel,
		})
		report.Directories++
		for _, entry := range snapshot.Entries {
			target := path.Join(remoteRel, entry.GetPath())
			change, body, err := changeFor(entry, filepath.Join(localPath, filepath.FromSlash(entry.GetPath())), target, report)
			if err != nil {
				return nil, err
			}
			if change == nil {
				continue
			}
			if body != "" {
				bodies[entry.GetDigest()] = body
			}
			changes = append(changes, change)
		}
		for _, problem := range snapshot.Problems {
			report.Skipped = append(report.Skipped, fmt.Sprintf("%s: %s", problem.GetPath(), problem.GetMessage()))
		}
	} else {
		entry, err := entryForFile(localPath, path.Base(remoteRel))
		if err != nil {
			return nil, err
		}
		change, body, err := changeFor(entry, localPath, remoteRel, report)
		if err != nil {
			return nil, err
		}
		if change == nil {
			return nil, fmt.Errorf("%s is not a regular file or symlink", localPath)
		}
		if body != "" {
			bodies[entry.GetDigest()] = body
		}
		changes = append(changes, change)
	}

	if err := stageBodies(c, bodies, report); err != nil {
		return nil, err
	}
	// Directories before the files inside them, so a transition never has to
	// guess at ordering.
	sort.SliceStable(changes, func(i, j int) bool {
		return changeRank(changes[i]) < changeRank(changes[j])
	})
	results, err := c.Transition(changes)
	if err != nil {
		return nil, err
	}
	return report, firstFailure(results)
}

func changeRank(change *syncv1.Change) int {
	if change.GetKind() == syncv1.ChangeKind_CHANGE_KIND_CREATE_DIRECTORY {
		return strings.Count(change.GetPath(), "/")
	}
	// Files and links after every directory.
	return 1 << 20
}

func changeFor(entry *syncv1.Entry, localPath, remoteRel string, report *Report) (*syncv1.Change, string, error) {
	switch entry.GetKind() {
	case syncv1.EntryKind_ENTRY_KIND_DIRECTORY:
		report.Directories++
		return &syncv1.Change{Kind: syncv1.ChangeKind_CHANGE_KIND_CREATE_DIRECTORY, Path: remoteRel}, "", nil
	case syncv1.EntryKind_ENTRY_KIND_FILE:
		report.Files++
		report.Bytes += entry.GetSize()
		return &syncv1.Change{
			Kind:       syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE,
			Path:       remoteRel,
			Digest:     entry.GetDigest(),
			Executable: entry.GetExecutable(),
		}, localPath, nil
	case syncv1.EntryKind_ENTRY_KIND_SYMLINK:
		return &syncv1.Change{
			Kind:   syncv1.ChangeKind_CHANGE_KIND_CREATE_SYMLINK,
			Path:   remoteRel,
			Target: entry.GetTarget(),
		}, "", nil
	default:
		report.Skipped = append(report.Skipped, entry.GetPath())
		return nil, "", nil
	}
}

// stageBodies sends only what the endpoint does not already hold, and checks
// there is room before writing rather than discovering ENOSPC partway through.
func stageBodies(c *client.Client, bodies map[string]string, report *Report) error {
	if len(bodies) == 0 {
		return nil
	}
	digests := make([]string, 0, len(bodies))
	for digest := range bodies {
		digests = append(digests, digest)
	}
	sort.Strings(digests)

	query, err := c.StageQuery(digests)
	if err != nil {
		return err
	}
	if available := query.GetAvailableBytes(); available >= 0 && available < report.Bytes {
		return fmt.Errorf("the destination has %d bytes free, this copy needs %d", available, report.Bytes)
	}
	for _, digest := range query.GetMissing() {
		localPath, ok := bodies[digest]
		if !ok {
			return fmt.Errorf("endpoint asked for content %s that was not offered", digest)
		}
		if err := pushOne(c, digest, localPath); err != nil {
			return err
		}
	}
	return nil
}

func pushOne(c *client.Client, digest, localPath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return c.StagePut(digest, info.Size(), file)
}

// Pull copies remoteRel out of the endpoint's root to localPath.
func Pull(c *client.Client, remoteRel, localPath string, recursive bool) (*Report, error) {
	scan, err := c.Scan(nil)
	if err != nil {
		return nil, err
	}

	matches := make([]*syncv1.Entry, 0, len(scan.GetEntries()))
	prefix := remoteRel + "/"
	for _, entry := range scan.GetEntries() {
		if entry.GetPath() == remoteRel || strings.HasPrefix(entry.GetPath(), prefix) {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%s not found in the sandbox", remoteRel)
	}
	if len(matches) > 1 && !recursive {
		return nil, fmt.Errorf("%s is a directory (use -r)", remoteRel)
	}
	if len(matches) == 1 && matches[0].GetKind() == syncv1.EntryKind_ENTRY_KIND_DIRECTORY && !recursive {
		return nil, fmt.Errorf("%s is a directory (use -r)", remoteRel)
	}

	report := &Report{}
	for _, entry := range matches {
		destination, err := destinationFor(entry.GetPath(), remoteRel, localPath)
		if err != nil {
			return nil, err
		}
		if err := pullOne(c, entry, destination, report); err != nil {
			return nil, err
		}
	}
	for _, problem := range scan.GetProblems() {
		report.Skipped = append(report.Skipped, fmt.Sprintf("%s: %s", problem.GetPath(), problem.GetMessage()))
	}
	return report, nil
}

// destinationFor maps a remote path onto the local tree. Copying a directory
// reproduces its shape underneath localPath; copying one file writes it there.
func destinationFor(remotePath, remoteRel, localPath string) (string, error) {
	if remotePath == remoteRel {
		return localPath, nil
	}
	rel := strings.TrimPrefix(remotePath, remoteRel+"/")
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("endpoint returned a path escaping the copy: %s", remotePath)
	}
	return filepath.Join(localPath, cleaned), nil
}

func pullOne(c *client.Client, entry *syncv1.Entry, destination string, report *Report) error {
	switch entry.GetKind() {
	case syncv1.EntryKind_ENTRY_KIND_DIRECTORY:
		report.Directories++
		return os.MkdirAll(destination, 0o755)
	case syncv1.EntryKind_ENTRY_KIND_SYMLINK:
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Symlink(filepath.FromSlash(entry.GetTarget()), destination)
	case syncv1.EntryKind_ENTRY_KIND_FILE:
		report.Files++
		report.Bytes += entry.GetSize()
		return pullFile(c, entry, destination)
	default:
		report.Skipped = append(report.Skipped, entry.GetPath())
		return nil
	}
}

// pullFile writes through a temporary beside the destination and renames, so an
// interrupted copy never leaves a partial file where a whole one is expected.
func pullFile(c *client.Client, entry *syncv1.Entry, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".agyn-cp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() {
		temp.Close()
		os.Remove(tempName)
	}()

	if _, err := c.Supply(entry.GetPath(), entry.GetDigest(), temp); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if entry.GetExecutable() {
		mode = 0o755
	}
	if err := os.Chmod(tempName, mode); err != nil {
		return err
	}
	return os.Rename(tempName, destination)
}

func entryForFile(localPath, name string) (*syncv1.Entry, error) {
	info, err := os.Lstat(localPath)
	if err != nil {
		return nil, err
	}
	entry := &syncv1.Entry{Path: name, ModifiedUnixNano: info.ModTime().UnixNano()}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(localPath)
		if err != nil {
			return nil, err
		}
		entry.Kind = syncv1.EntryKind_ENTRY_KIND_SYMLINK
		entry.Target = filepath.ToSlash(target)
	case info.Mode().IsRegular():
		digest, err := tree.DigestFile(localPath)
		if err != nil {
			return nil, err
		}
		entry.Kind = syncv1.EntryKind_ENTRY_KIND_FILE
		entry.Size = info.Size()
		entry.Executable = info.Mode()&0o100 != 0
		entry.Digest = digest
	default:
		entry.Kind = syncv1.EntryKind_ENTRY_KIND_UNSUPPORTED
	}
	return entry, nil
}

func firstFailure(results []*syncv1.Result) error {
	for _, result := range results {
		if !result.GetApplied() {
			return fmt.Errorf("%s: %s", result.GetPath(), result.GetMessage())
		}
	}
	return nil
}

// Discard is an io.Writer sink used when a body must be drained but not kept.
var Discard io.Writer = io.Discard
