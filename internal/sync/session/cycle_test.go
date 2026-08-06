package session

import (
	"os"
	"path/filepath"
	"testing"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/internal/sync/reconcile"
)

func entries(n int) []*syncv1.Entry {
	out := make([]*syncv1.Entry, 0, n)
	for i := range n {
		out = append(out, &syncv1.Entry{
			Path:   filepath.Join("f", string(rune('a'+i%26)), string(rune('a'+i/26))),
			Kind:   syncv1.EntryKind_ENTRY_KIND_FILE,
			Digest: "00",
		})
	}
	return out
}

func ancestorOf(n int) *reconcile.Result {
	both := entries(n)
	return reconcile.Reconcile(nil, both, both)
}

// /workspace has no trash, so a deletion headed into a sandbox is gated rather
// than merely reversible. Losing most of a tracked tree is not an edit.
func TestContentLossHaltsRatherThanPropagating(t *testing.T) {
	base := ancestorOf(40)

	// The local side comes back nearly empty — a partial unmount, a
	// half-restored backup, an interrupted checkout.
	halt := guardContentLoss(base.Ancestor, entries(2), entries(40))
	if halt == nil {
		t.Fatal("a local root that lost most of its content did not halt")
	}
	if halt.Reason != HaltContentLoss {
		t.Fatalf("unexpected halt reason %s", halt.Reason)
	}

	// The guard is symmetric: the sandbox side is protected too.
	if halt := guardContentLoss(base.Ancestor, entries(40), entries(2)); halt == nil {
		t.Fatal("a sandbox workspace that lost most of its content did not halt")
	}
}

// An ordinary edit must not trip it, or the halt stops carrying signal.
func TestOrdinaryDeletionsDoNotHalt(t *testing.T) {
	base := ancestorOf(40)
	if halt := guardContentLoss(base.Ancestor, entries(35), entries(40)); halt != nil {
		t.Fatalf("a routine deletion halted the session: %v", halt)
	}
	// A small tree is left alone entirely: losing 3 of 4 files is routine.
	small := ancestorOf(4)
	if halt := guardContentLoss(small.Ancestor, entries(1), entries(4)); halt != nil {
		t.Fatalf("a small tree halted on a routine edit: %v", halt)
	}
}

// With no ancestor there is nothing to have lost — a first pass carries both
// sides rather than reading an empty side as a deletion.
func TestFirstPassDoesNotHalt(t *testing.T) {
	if halt := guardContentLoss(nil, nil, entries(100)); halt != nil {
		t.Fatalf("a first pass halted: %v", halt)
	}
}

// The inode identifies the directory itself. A directory deleted and recreated
// is a different one, and would otherwise scan as an empty tree.
func TestRootIdentityCatchesARecreatedDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "project")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	inode, err := RootInode(target)
	if err != nil {
		t.Skipf("inode unavailable: %v", err)
	}

	cycle := &Cycle{State: &State{LocalRoot: target, LocalRootInode: inode}}
	if err := cycle.checkLocalIdentity(); err != nil {
		t.Fatalf("an unchanged directory failed its identity check: %v", err)
	}

	if err := os.RemoveAll(target); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("recreate: %v", err)
	}

	err = cycle.checkLocalIdentity()
	if err == nil {
		t.Fatal("a recreated directory passed the identity check")
	}
	halt, ok := err.(*Halt)
	if !ok || halt.Reason != HaltRootReplaced {
		t.Fatalf("expected a root-replaced halt, got %v", err)
	}
}

func TestRootIdentityCatchesAVanishedDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-mounted")
	cycle := &Cycle{State: &State{LocalRoot: root, LocalRootInode: 12345}}
	if err := cycle.checkLocalIdentity(); err == nil {
		t.Fatal("a missing local root passed the identity check")
	}
}

// Two engines writing one subtree cannot be reconciled.
func TestOverlappingRootsAreDetected(t *testing.T) {
	if !Overlaps("/home/me/proj", "/home/me/proj") {
		t.Fatal("identical roots not detected")
	}
	if !Overlaps("/home/me/proj", "/home/me/proj/sub") {
		t.Fatal("nested root not detected")
	}
	if !Overlaps("/home/me/proj/sub", "/home/me/proj") {
		t.Fatal("nesting not detected in the other direction")
	}
	if Overlaps("/home/me/proj", "/home/me/project") {
		t.Fatal("a shared prefix is not nesting")
	}
	if Overlaps("/home/me/a", "/home/me/b") {
		t.Fatal("siblings reported as overlapping")
	}
}

// Identity is the triple; the name is a label that steps aside on collision.
func TestSessionNameTakesADiscriminatorOnCollision(t *testing.T) {
	taken := map[string]bool{"api-brave-otter": true}
	name := NameFor("/home/me/api", "brave-otter", func(candidate string) bool { return taken[candidate] })
	if name == "api-brave-otter" {
		t.Fatal("collided with an existing session name")
	}
	if name != "api-brave-otter-2" {
		t.Fatalf("unexpected discriminator: %s", name)
	}
}

// A base written by a newer CLI is discarded rather than misparsed, so the next
// cycle re-derives from both sides instead of guessing.
func TestUnreadableAncestorIsDiscarded(t *testing.T) {
	store := NewStore(t.TempDir())
	id := "session-1"
	if err := os.MkdirAll(store.Dir(id), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(store.ancestorPath(id), []byte(`{"format":999,"tree":"AA=="}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if ancestor := store.LoadAncestor(id); ancestor != nil {
		t.Fatal("an unrecognized ancestor format was used instead of discarded")
	}
}

func TestAncestorRoundTrips(t *testing.T) {
	store := NewStore(t.TempDir())
	base := ancestorOf(12)
	if err := store.SaveAncestor("s", base.Ancestor); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded := store.LoadAncestor("s")
	if loaded == nil {
		t.Fatal("ancestor did not round-trip")
	}
	if countEntries(loaded) != countEntries(base.Ancestor) {
		t.Fatalf("ancestor lost entries: %d vs %d", countEntries(loaded), countEntries(base.Ancestor))
	}
}
