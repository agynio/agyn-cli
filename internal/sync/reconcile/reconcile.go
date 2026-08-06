// Package reconcile adapts the flat snapshots the endpoint protocol carries to
// the nested tree Mutagen's reconciler operates on, and converts the changes it
// produces back into the transition the protocol applies.
//
// The reconciliation itself is Mutagen's. What lives here is the shape
// conversion and the platform's own rules: two-way-safe, so a path changed on
// both sides is quarantined rather than resolved by guessing.
package reconcile

import (
	"encoding/hex"
	"path"
	"sort"
	"strings"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/third_party/mutagen/pkg/synchronization/core"
)

// Mode is two-way-safe: neither side wins automatically. A path changed on both
// sides becomes a conflict, which quarantines that path and leaves everything
// else syncing — the alternative resolves by policy and silently discards work.
const Mode = core.SynchronizationMode_SynchronizationModeTwoWaySafe

// Result is one reconciliation pass.
type Result struct {
	// ToLocal and ToRemote are the changes each side must apply.
	ToLocal  []*syncv1.Change
	ToRemote []*syncv1.Change
	// Conflicts are paths changed on both sides. They are quarantined, not
	// resolved: both versions survive and unrelated work is not held hostage.
	Conflicts []Conflict
	// Ancestor is the agreed state to record once the cycle completes. It is
	// committed only after both sides have applied, so an interruption
	// recomputes on the next pass rather than recording a state that is not
	// true on disk.
	Ancestor *core.Entry
}

// Conflict names a path both sides changed, with a human-readable account of
// what each did.
type Conflict struct {
	Path         string
	LocalChange  string
	RemoteChange string
}

// Snapshot is a flat scan from either side.
type Snapshot []*syncv1.Entry

// Reconcile computes what each side must do to agree, given the ancestor from
// the last completed cycle.
func Reconcile(ancestor *core.Entry, local, remote Snapshot) *Result {
	alpha := treeFrom(local)
	beta := treeFrom(remote)

	ancestorChanges, alphaChanges, betaChanges, conflicts := core.Reconcile(ancestor, alpha, beta, Mode)

	newAncestor, err := core.Apply(ancestor, ancestorChanges)
	if err != nil {
		// Apply only fails on changes it did not itself produce. Falling back
		// to the old ancestor recomputes next cycle rather than recording a
		// state that may not be true on disk.
		newAncestor = ancestor
	}

	return &Result{
		ToLocal:   changesFrom(alphaChanges),
		ToRemote:  changesFrom(betaChanges),
		Conflicts: conflictsFrom(conflicts),
		Ancestor:  newAncestor,
	}
}

// treeFrom builds the nested entry tree from a flat scan. Intermediate
// directories are synthesized: a scan lists them, but a snapshot that lost one
// would strand its children.
func treeFrom(snapshot Snapshot) *core.Entry {
	root := &core.Entry{Kind: core.EntryKind_Directory, Contents: map[string]*core.Entry{}}
	sorted := append(Snapshot(nil), snapshot...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].GetPath() < sorted[j].GetPath() })

	for _, entry := range sorted {
		converted := entryFrom(entry)
		if converted == nil {
			continue
		}
		parent := root
		parts := strings.Split(entry.GetPath(), "/")
		for _, part := range parts[:len(parts)-1] {
			child, ok := parent.Contents[part]
			if !ok || child.Kind != core.EntryKind_Directory {
				child = &core.Entry{Kind: core.EntryKind_Directory, Contents: map[string]*core.Entry{}}
				parent.Contents[part] = child
			}
			if child.Contents == nil {
				child.Contents = map[string]*core.Entry{}
			}
			parent = child
		}
		name := parts[len(parts)-1]
		// A directory already synthesized as a parent keeps the children it
		// collected.
		if existing, ok := parent.Contents[name]; ok && converted.Kind == core.EntryKind_Directory {
			existing.Kind = core.EntryKind_Directory
			continue
		}
		parent.Contents[name] = converted
	}
	return root
}

func entryFrom(entry *syncv1.Entry) *core.Entry {
	switch entry.GetKind() {
	case syncv1.EntryKind_ENTRY_KIND_DIRECTORY:
		return &core.Entry{Kind: core.EntryKind_Directory, Contents: map[string]*core.Entry{}}
	case syncv1.EntryKind_ENTRY_KIND_FILE:
		digest, err := hex.DecodeString(entry.GetDigest())
		if err != nil {
			return nil
		}
		return &core.Entry{
			Kind:       core.EntryKind_File,
			Digest:     digest,
			Executable: entry.GetExecutable(),
		}
	case syncv1.EntryKind_ENTRY_KIND_SYMLINK:
		return &core.Entry{Kind: core.EntryKind_SymbolicLink, Target: entry.GetTarget()}
	default:
		// Sockets, devices, and anything unreadable are left out of the tree
		// entirely rather than represented as something they are not.
		return nil
	}
}

// changesFrom flattens Mutagen's nested changes into the per-path operations
// the transition applies, ordered so a directory always precedes its contents
// and a removal always precedes what replaces it.
func changesFrom(changes []*core.Change) []*syncv1.Change {
	var flat []*syncv1.Change
	for _, change := range changes {
		flat = append(flat, flatten(change.GetPath(), change.GetOld(), change.GetNew())...)
	}
	sort.SliceStable(flat, func(i, j int) bool { return rank(flat[i]) < rank(flat[j]) })
	return flat
}

// rank orders a transition: removals first, then directories shallowest-first,
// then everything that lands inside them.
func rank(change *syncv1.Change) int {
	switch change.GetKind() {
	case syncv1.ChangeKind_CHANGE_KIND_REMOVE:
		// Deepest removal first, so a directory is emptied before it goes.
		return -1000000 + (1000 - strings.Count(change.GetPath(), "/"))
	case syncv1.ChangeKind_CHANGE_KIND_CREATE_DIRECTORY:
		return strings.Count(change.GetPath(), "/")
	default:
		return 1 << 20
	}
}

func flatten(base string, old, new *core.Entry) []*syncv1.Change {
	var changes []*syncv1.Change
	// Anything the old tree had that the new one does not is removed. Removing
	// the root of a subtree is enough; the transition removes recursively.
	if old != nil && (new == nil || new.Kind != old.Kind) {
		if base != "" {
			changes = append(changes, &syncv1.Change{Kind: syncv1.ChangeKind_CHANGE_KIND_REMOVE, Path: base})
		}
	}
	if new == nil {
		return changes
	}
	switch new.Kind {
	case core.EntryKind_Directory:
		if base != "" && (old == nil || old.Kind != core.EntryKind_Directory) {
			changes = append(changes, &syncv1.Change{Kind: syncv1.ChangeKind_CHANGE_KIND_CREATE_DIRECTORY, Path: base})
		}
		names := make([]string, 0, len(new.Contents))
		for name := range new.Contents {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			var oldChild *core.Entry
			if old != nil && old.Kind == core.EntryKind_Directory {
				oldChild = old.Contents[name]
			}
			changes = append(changes, flatten(path.Join(base, name), oldChild, new.Contents[name])...)
		}
		// A child the old tree had and the new one does not.
		if old != nil && old.Kind == core.EntryKind_Directory {
			gone := make([]string, 0)
			for name := range old.Contents {
				if _, ok := new.Contents[name]; !ok {
					gone = append(gone, name)
				}
			}
			sort.Strings(gone)
			for _, name := range gone {
				changes = append(changes, &syncv1.Change{
					Kind: syncv1.ChangeKind_CHANGE_KIND_REMOVE,
					Path: path.Join(base, name),
				})
			}
		}
	case core.EntryKind_File:
		digest := hex.EncodeToString(new.Digest)
		// Content unchanged and only the executable bit moved: chmod rather
		// than restage and rewrite the whole file.
		if old != nil && old.Kind == core.EntryKind_File && hex.EncodeToString(old.Digest) == digest {
			if old.Executable != new.Executable {
				changes = append(changes, &syncv1.Change{
					Kind:       syncv1.ChangeKind_CHANGE_KIND_SET_EXECUTABLE,
					Path:       base,
					Executable: new.Executable,
				})
			}
			break
		}
		changes = append(changes, &syncv1.Change{
			Kind:       syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE,
			Path:       base,
			Digest:     digest,
			Executable: new.Executable,
		})
	case core.EntryKind_SymbolicLink:
		if old == nil || old.Kind != core.EntryKind_SymbolicLink || old.Target != new.Target {
			changes = append(changes, &syncv1.Change{
				Kind:   syncv1.ChangeKind_CHANGE_KIND_CREATE_SYMLINK,
				Path:   base,
				Target: new.Target,
			})
		}
	}
	return changes
}

func conflictsFrom(conflicts []*core.Conflict) []Conflict {
	converted := make([]Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		converted = append(converted, Conflict{
			Path:         conflict.GetRoot(),
			LocalChange:  describe(conflict.GetAlphaChanges()),
			RemoteChange: describe(conflict.GetBetaChanges()),
		})
	}
	sort.Slice(converted, func(i, j int) bool { return converted[i].Path < converted[j].Path })
	return converted
}

func describe(changes []*core.Change) string {
	parts := make([]string, 0, len(changes))
	for _, change := range changes {
		switch {
		case change.GetOld() == nil && change.GetNew() != nil:
			parts = append(parts, "created")
		case change.GetOld() != nil && change.GetNew() == nil:
			parts = append(parts, "deleted")
		default:
			parts = append(parts, "modified")
		}
	}
	if len(parts) == 0 {
		return "changed"
	}
	return strings.Join(parts, ", ")
}

// DigestsNeeded returns the content digests a side must be given before the
// changes can be applied, so staging happens before the transition.
func DigestsNeeded(changes []*syncv1.Change) []string {
	seen := map[string]struct{}{}
	digests := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.GetKind() != syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE {
			continue
		}
		if _, ok := seen[change.GetDigest()]; ok {
			continue
		}
		seen[change.GetDigest()] = struct{}{}
		digests = append(digests, change.GetDigest())
	}
	sort.Strings(digests)
	return digests
}
