package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
)

func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func file(path, content string) *syncv1.Entry {
	return &syncv1.Entry{Path: path, Kind: syncv1.EntryKind_ENTRY_KIND_FILE, Digest: digest(content), Size: int64(len(content))}
}

func exe(path, content string) *syncv1.Entry {
	entry := file(path, content)
	entry.Executable = true
	return entry
}

func dir(path string) *syncv1.Entry {
	return &syncv1.Entry{Path: path, Kind: syncv1.EntryKind_ENTRY_KIND_DIRECTORY}
}

// find returns the change for a path, or nil.
func find(changes []*syncv1.Change, path string) *syncv1.Change {
	for _, change := range changes {
		if change.GetPath() == path {
			return change
		}
	}
	return nil
}

func paths(changes []*syncv1.Change) []string {
	out := make([]string, 0, len(changes))
	for _, change := range changes {
		out = append(out, change.GetPath())
	}
	return out
}

// A first pass with no ancestor has to move everything each side is missing,
// in both directions at once.
func TestFirstPassCarriesBothDirections(t *testing.T) {
	local := Snapshot{file("only-local.txt", "L")}
	remote := Snapshot{file("only-remote.txt", "R")}

	result := Reconcile(nil, local, remote)

	if find(result.ToRemote, "only-local.txt") == nil {
		t.Fatalf("local file not carried to the sandbox: %v", paths(result.ToRemote))
	}
	if find(result.ToLocal, "only-remote.txt") == nil {
		t.Fatalf("sandbox file not carried locally: %v", paths(result.ToLocal))
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unrelated files conflicted: %+v", result.Conflicts)
	}
}

// The ancestor is what distinguishes "created over there" from "deleted over
// here" — the same observation without it.
func TestDeletionPropagatesOnlyAgainstAnAncestor(t *testing.T) {
	both := Snapshot{file("shared.txt", "v1")}
	base := Reconcile(nil, both, both)

	// Deleted locally, untouched remotely.
	result := Reconcile(base.Ancestor, Snapshot{}, both)

	remove := find(result.ToRemote, "shared.txt")
	if remove == nil || remove.GetKind() != syncv1.ChangeKind_CHANGE_KIND_REMOVE {
		t.Fatalf("local deletion did not propagate: %+v", result.ToRemote)
	}
	if len(result.ToLocal) != 0 {
		t.Fatalf("deletion was undone instead of propagated: %v", paths(result.ToLocal))
	}
}

// Both sides changing one path is the case that must not be resolved by
// guessing: quarantine that path, keep everything else moving.
func TestSamePathChangedBothSidesConflictsAndSparesTheRest(t *testing.T) {
	base := Reconcile(nil, Snapshot{file("doc.txt", "v1")}, Snapshot{file("doc.txt", "v1")})

	local := Snapshot{file("doc.txt", "local edit"), file("untouched.txt", "L")}
	remote := Snapshot{file("doc.txt", "remote edit")}
	result := Reconcile(base.Ancestor, local, remote)

	if len(result.Conflicts) != 1 || result.Conflicts[0].Path != "doc.txt" {
		t.Fatalf("expected doc.txt to conflict, got %+v", result.Conflicts)
	}
	if find(result.ToLocal, "doc.txt") != nil || find(result.ToRemote, "doc.txt") != nil {
		t.Fatal("a conflicted path was also scheduled for change")
	}
	// The whole point of per-path conflicts: unrelated work is not held hostage.
	if find(result.ToRemote, "untouched.txt") == nil {
		t.Fatalf("unrelated file was held back by the conflict: %v", paths(result.ToRemote))
	}
}

// Ownership never crosses; the executable bit does. Flipping it must not
// restage and rewrite the whole file.
func TestExecutableBitAloneIsAChmodNotARewrite(t *testing.T) {
	base := Reconcile(nil, Snapshot{file("tool.sh", "#!/bin/sh")}, Snapshot{file("tool.sh", "#!/bin/sh")})

	local := Snapshot{exe("tool.sh", "#!/bin/sh")}
	remote := Snapshot{file("tool.sh", "#!/bin/sh")}
	result := Reconcile(base.Ancestor, local, remote)

	change := find(result.ToRemote, "tool.sh")
	if change == nil {
		t.Fatalf("executable bit did not propagate: %v", paths(result.ToRemote))
	}
	if change.GetKind() != syncv1.ChangeKind_CHANGE_KIND_SET_EXECUTABLE {
		t.Fatalf("expected a chmod, got %s", change.GetKind())
	}
	if !change.GetExecutable() {
		t.Fatal("chmod did not carry the bit")
	}
}

// A transition applies in order, so a directory must precede what lands inside
// it and a removal must precede whatever replaces it.
func TestChangesAreOrderedForApplication(t *testing.T) {
	local := Snapshot{dir("a"), dir("a/b"), file("a/b/deep.txt", "x"), file("top.txt", "y")}
	result := Reconcile(nil, local, Snapshot{})

	seen := map[string]int{}
	for i, change := range result.ToRemote {
		seen[change.GetPath()] = i
	}
	if seen["a"] > seen["a/b"] || seen["a/b"] > seen["a/b/deep.txt"] {
		t.Fatalf("directories not ordered before their contents: %v", paths(result.ToRemote))
	}
}

func TestRemovalsGoDeepestFirst(t *testing.T) {
	tree := Snapshot{dir("a"), dir("a/b"), file("a/b/deep.txt", "x")}
	base := Reconcile(nil, tree, tree)

	result := Reconcile(base.Ancestor, Snapshot{}, tree)

	order := paths(result.ToRemote)
	deep, shallow := -1, -1
	for i, p := range order {
		if p == "a/b/deep.txt" {
			deep = i
		}
		if p == "a" {
			shallow = i
		}
	}
	if deep >= 0 && shallow >= 0 && deep > shallow {
		t.Fatalf("shallow removal ordered before the deep one: %v", order)
	}
}

// Staging happens before the transition, so the cycle has to know what content
// it needs before it applies anything.
func TestDigestsNeededCoversOnlyNewContent(t *testing.T) {
	changes := []*syncv1.Change{
		{Kind: syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE, Path: "a.txt", Digest: digest("a")},
		{Kind: syncv1.ChangeKind_CHANGE_KIND_CREATE_FILE, Path: "copy.txt", Digest: digest("a")},
		{Kind: syncv1.ChangeKind_CHANGE_KIND_CREATE_DIRECTORY, Path: "d"},
		{Kind: syncv1.ChangeKind_CHANGE_KIND_REMOVE, Path: "gone.txt"},
	}
	needed := DigestsNeeded(changes)
	if len(needed) != 1 || needed[0] != digest("a") {
		t.Fatalf("expected one digest for two files sharing content, got %v", needed)
	}
}

// Sockets and devices cannot be carried. Leaving them out of the tree is not
// the same as reporting them as missing, which would delete the other side's.
func TestUnsupportedEntriesAreNotCarried(t *testing.T) {
	local := Snapshot{
		{Path: "sock", Kind: syncv1.EntryKind_ENTRY_KIND_UNSUPPORTED},
		file("real.txt", "x"),
	}
	result := Reconcile(nil, local, Snapshot{})

	if find(result.ToRemote, "sock") != nil {
		t.Fatalf("an unsupported entry was scheduled: %v", paths(result.ToRemote))
	}
	if find(result.ToRemote, "real.txt") == nil {
		t.Fatal("a regular file beside an unsupported entry was dropped")
	}
}

func TestSymlinkTargetsCross(t *testing.T) {
	local := Snapshot{{Path: "link", Kind: syncv1.EntryKind_ENTRY_KIND_SYMLINK, Target: "target.txt"}}
	result := Reconcile(nil, local, Snapshot{})

	change := find(result.ToRemote, "link")
	if change == nil || change.GetKind() != syncv1.ChangeKind_CHANGE_KIND_CREATE_SYMLINK {
		t.Fatalf("symlink not carried: %+v", result.ToRemote)
	}
	if change.GetTarget() != "target.txt" {
		t.Fatalf("symlink target lost, got %q", change.GetTarget())
	}
}

// A completed cycle records the agreed state, which is what makes the next
// pass able to tell a creation from a deletion.
func TestAncestorRecordsTheAgreedState(t *testing.T) {
	both := Snapshot{file("a.txt", "v1"), dir("d"), file("d/b.txt", "v2")}
	result := Reconcile(nil, both, both)

	if result.Ancestor == nil || len(result.Ancestor.Contents) == 0 {
		t.Fatal("no ancestor recorded for an agreed pair")
	}
	// Nothing to do when both sides already agree.
	if len(result.ToLocal) != 0 || len(result.ToRemote) != 0 {
		t.Fatalf("agreeing sides produced work: local=%v remote=%v",
			paths(result.ToLocal), paths(result.ToRemote))
	}
	if _, ok := result.Ancestor.Contents["d"]; !ok {
		t.Fatalf("ancestor lost a directory: %v", keys(result.Ancestor.Contents))
	}
}

func keys[V any](m map[string]V) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ",")
}
