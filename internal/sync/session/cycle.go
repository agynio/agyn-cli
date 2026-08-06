package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/internal/sync/client"
	"github.com/agynio/agyn-cli/internal/sync/reconcile"
	"github.com/agynio/agyn-cli/internal/sync/tree"
	"github.com/agynio/agyn-cli/third_party/mutagen/pkg/synchronization/core"
)

// contentLossThreshold is the fraction of tracked entries a side may lose in
// one cycle before the session halts instead of propagating it.
//
// This is the load-bearing guard, not a supplement: the inode check catches a
// root that disappeared wholesale, and this catches everything it cannot see —
// a partial unmount, a half-restored backup, an interrupted checkout, an inode
// reused after a delete-and-recreate, or a different filesystem mounted at the
// same path whose root inode collides.
const contentLossThreshold = 0.5

// contentLossFloor keeps small trees usable. Losing 3 of 4 files is a routine
// edit; losing 3000 of 4000 is not, and only the second is worth halting over.
const contentLossFloor = 10

// Halt is a durable stop. Nothing has been applied, and it is safe to remain in
// indefinitely — which is what makes it acceptable for a background process
// that may never be attended to.
type Halt struct {
	Reason HaltReason
	Detail string
}

func (h *Halt) Error() string { return fmt.Sprintf("%s: %s", h.Reason, h.Detail) }

// Cycle is one pass: identity, scan, guard, reconcile, stage, transition,
// supply, commit.
type Cycle struct {
	State    *State
	Store    *Store
	Endpoint *client.Client
}

// Outcome reports what a completed cycle did.
type Outcome struct {
	AppliedLocal  int
	AppliedRemote int
	Conflicts     []Conflict
}

// Run executes one cycle. A returned *Halt is a durable state, not a transient
// failure: the caller records it and stops rather than retrying.
func (c *Cycle) Run() (*Outcome, error) {
	if err := c.checkLocalIdentity(); err != nil {
		return nil, err
	}

	handshake, err := c.Endpoint.Handshake(syncv1.MarkerMode_MARKER_MODE_CREATE, c.State.ID)
	if err != nil {
		return nil, c.classify(err)
	}
	// A wiped or unrecognized remote root halts before anything is read or
	// written. A sandbox terminated by TTL returns an empty /workspace, and
	// mirroring that would delete the engineer's work.
	if marker := handshake.GetSessionId(); marker != "" && marker != c.State.ID {
		return nil, &Halt{
			Reason: HaltRootReplaced,
			Detail: fmt.Sprintf("the sandbox workspace belongs to session %s, not %s", marker, c.State.ID),
		}
	}

	local, err := tree.Scan(c.State.LocalRoot, nil)
	if err != nil {
		return nil, err
	}
	remote, err := c.Endpoint.Scan(nil)
	if err != nil {
		return nil, c.classify(err)
	}

	ancestor := c.Store.LoadAncestor(c.State.ID)
	if halt := guardContentLoss(ancestor, local.Entries, remote.GetEntries()); halt != nil {
		return nil, halt
	}

	result := reconcile.Reconcile(ancestor, local.Entries, remote.GetEntries())

	if err := c.applyRemote(result.ToRemote); err != nil {
		return nil, err
	}
	if err := c.applyLocal(result.ToLocal); err != nil {
		return nil, err
	}

	// The ancestor is committed only after a complete cycle, so an interruption
	// at any point simply recomputes on the next pass.
	if err := c.Store.SaveAncestor(c.State.ID, result.Ancestor); err != nil {
		return nil, err
	}
	c.State.LastSync = time.Now().UTC()
	c.State.Conflicts = conflictsFrom(result.Conflicts)

	return &Outcome{
		AppliedLocal:  len(result.ToLocal),
		AppliedRemote: len(result.ToRemote),
		Conflicts:     c.State.Conflicts,
	}, nil
}

func conflictsFrom(conflicts []reconcile.Conflict) []Conflict {
	converted := make([]Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		converted = append(converted, Conflict{
			Path:         conflict.Path,
			LocalChange:  conflict.LocalChange,
			RemoteChange: conflict.RemoteChange,
		})
	}
	return converted
}

// checkLocalIdentity is the cheap fast path over the content-loss guard: a root
// that is no longer the same directory is caught before anything is scanned.
func (c *Cycle) checkLocalIdentity() error {
	inode, err := RootInode(c.State.LocalRoot)
	if err != nil {
		return &Halt{Reason: HaltRootReplaced, Detail: fmt.Sprintf("local root %s: %v", c.State.LocalRoot, err)}
	}
	if c.State.LocalRootInode != 0 && inode != c.State.LocalRootInode {
		return &Halt{
			Reason: HaltRootReplaced,
			Detail: fmt.Sprintf("%s is no longer the directory this session was created for", c.State.LocalRoot),
		}
	}
	return nil
}

// guardContentLoss halts a side that lost most of its tracked content since the
// last cycle rather than propagating the loss to the other side. /workspace has
// no trash, so a deletion headed into a sandbox is gated rather than merely
// reversible.
func guardContentLoss(ancestor *core.Entry, local, remote []*syncv1.Entry) *Halt {
	if ancestor == nil {
		return nil
	}
	tracked := countEntries(ancestor)
	if tracked < contentLossFloor {
		return nil
	}
	if lost := lostFraction(tracked, len(local)); lost > contentLossThreshold {
		return &Halt{
			Reason: HaltContentLoss,
			Detail: fmt.Sprintf("the local root lost %d of %d tracked entries since the last sync", tracked-len(local), tracked),
		}
	}
	if lost := lostFraction(tracked, len(remote)); lost > contentLossThreshold {
		return &Halt{
			Reason: HaltContentLoss,
			Detail: fmt.Sprintf("the sandbox workspace lost %d of %d tracked entries since the last sync", tracked-len(remote), tracked),
		}
	}
	return nil
}

func lostFraction(tracked, present int) float64 {
	if tracked == 0 || present >= tracked {
		return 0
	}
	return float64(tracked-present) / float64(tracked)
}

func countEntries(entry *core.Entry) int {
	if entry == nil {
		return 0
	}
	count := 0
	for _, child := range entry.Contents {
		count += 1 + countEntries(child)
	}
	return count
}

// applyRemote stages the content the sandbox needs, then applies. Staging comes
// first so a transition never references content that is not already there.
func (c *Cycle) applyRemote(changes []*syncv1.Change) error {
	if len(changes) == 0 {
		return nil
	}
	digests := reconcile.DigestsNeeded(changes)
	if len(digests) > 0 {
		query, err := c.Endpoint.StageQuery(digests)
		if err != nil {
			return c.classify(err)
		}
		byDigest := map[string]string{}
		for _, change := range changes {
			if change.GetKind() == syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE {
				byDigest[change.GetDigest()] = filepath.Join(c.State.LocalRoot, filepath.FromSlash(change.GetPath()))
			}
		}
		for _, digest := range query.GetMissing() {
			path, ok := byDigest[digest]
			if !ok {
				return fmt.Errorf("endpoint asked for content %s that was not offered", digest)
			}
			if err := c.stageOne(digest, path); err != nil {
				return err
			}
		}
	}
	results, err := c.Endpoint.Transition(changes)
	if err != nil {
		return c.classify(err)
	}
	// A failure on one path does not abort the batch. Unapplied paths simply
	// stay unreconciled and the next cycle recomputes them, so they are
	// surfaced rather than treated as fatal.
	c.State.StatusNote = unappliedNote(results)
	return nil
}

func (c *Cycle) stageOne(digest, path string) error {
	file, err := os.Open(path)
	if err != nil {
		// The file moved under the scan. The next cycle sees the new state.
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return c.Endpoint.StagePut(digest, info.Size(), file)
}

// applyLocal fetches what the local side needs and applies it through the same
// staged atomic write. Deletions go to the session's trash rather than being
// unlinked: a recoverable action needs no confirmation.
func (c *Cycle) applyLocal(changes []*syncv1.Change) error {
	if len(changes) == 0 {
		return nil
	}
	staging, err := tree.OpenStaging(c.State.LocalRoot)
	if err != nil {
		return err
	}
	for _, change := range changes {
		if change.GetKind() != syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE {
			continue
		}
		if staging.Has(change.GetDigest()) {
			continue
		}
		if err := c.supplyOne(staging, change); err != nil {
			return err
		}
	}
	trash := c.Store.TrashDir(c.State.ID)
	for _, change := range changes {
		if change.GetKind() == syncv1.ChangeKind_CHANGE_KIND_REMOVE {
			if err := trashPath(c.State.LocalRoot, change.GetPath(), trash); err != nil {
				return err
			}
		}
	}
	tree.Transition(c.State.LocalRoot, staging, changes)
	return nil
}

func (c *Cycle) supplyOne(staging *tree.Staging, change *syncv1.Change) error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		_, err := staging.Put(change.GetDigest(), reader, -1)
		reader.Close()
		done <- err
	}()
	_, supplyErr := c.Endpoint.Supply(change.GetPath(), change.GetDigest(), writer)
	writer.Close()
	if err := <-done; err != nil {
		return err
	}
	if supplyErr != nil {
		return c.classify(supplyErr)
	}
	return nil
}

// trashPath moves a local deletion aside instead of unlinking it. An
// unrecoverable action needs a human who may not be present.
func trashPath(root, rel, trash string) error {
	source := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Lstat(source); err != nil {
		return nil
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	destination := filepath.Join(trash, stamp, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		// Across filesystems a rename fails; leaving the file in place is
		// safer than removing something that cannot be recovered.
		return nil
	}
	return nil
}

// classify turns an endpoint or transport failure into the outcome the
// reconnection model names, so the caller can tell a halt from a retry.
func (c *Cycle) classify(err error) error {
	var endpointErr *client.Error
	if errors.As(err, &endpointErr) {
		switch endpointErr.Code {
		case syncv1.ErrorCode_ERROR_CODE_PROTOCOL_VERSION:
			return &Halt{Reason: HaltVersionGap, Detail: endpointErr.Message}
		case syncv1.ErrorCode_ERROR_CODE_ROOT_OUTSIDE_WORKSPACE, syncv1.ErrorCode_ERROR_CODE_ROOT_INVALID:
			return &Halt{Reason: HaltRootReplaced, Detail: endpointErr.Message}
		}
	}
	return err
}

// unappliedNote summarizes paths the far side could not apply, for the status
// command to show.
func unappliedNote(results []*syncv1.Result) string {
	var failed []string
	for _, result := range results {
		if !result.GetApplied() {
			failed = append(failed, fmt.Sprintf("%s: %s", result.GetPath(), result.GetMessage()))
		}
	}
	if len(failed) == 0 {
		return ""
	}
	if len(failed) > 3 {
		return fmt.Sprintf("%s (and %d more)", strings.Join(failed[:3], "; "), len(failed)-3)
	}
	return strings.Join(failed, "; ")
}
