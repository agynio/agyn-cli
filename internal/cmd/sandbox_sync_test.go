package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agynio/agyn-cli/internal/sync/session"
)

// `sandbox start --sync` must refuse before it creates anything. Failing after
// creation leaves a sandbox nobody asked for, running and billable.
func TestSyncRootIsCheckedBeforeAnythingIsCreated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Sessions record the symlink-resolved root, which is what the check
	// compares against; on macOS t.TempDir() is a symlink into /private.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	store := session.NewStore(filepath.Join(home, ".agyn", "sync"))
	inode, inodeErr := session.RootInode(root)
	if inodeErr != nil {
		t.Skipf("inode unavailable: %v", inodeErr)
	}
	if err := store.Save(&session.State{
		ID: "existing", Name: "notes-brave-otter", LocalRoot: root,
		LocalRootInode: inode, Sandbox: "brave-otter", RemoteRoot: "/workspace",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err = checkSyncLocalRoot(context.Background(), nil, root)
	if err == nil {
		t.Fatal("a directory already synced was accepted")
	}
	// The message has to name the way out: the blocking session may be one the
	// engineer forgot, for a sandbox that no longer exists.
	if !strings.Contains(err.Error(), "sync stop notes-brave-otter") {
		t.Fatalf("error does not say how to clear it: %v", err)
	}

	// A nested directory is refused for the same reason, worded differently.
	nested := filepath.Join(root, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := checkSyncLocalRoot(context.Background(), nil, nested); err == nil {
		t.Fatal("a nested directory was accepted")
	}

	// An unrelated directory is fine.
	if _, err := checkSyncLocalRoot(context.Background(), nil, t.TempDir()); err != nil {
		t.Fatalf("an unrelated directory was refused: %v", err)
	}
}

// A session whose sandbox has been deleted can never sync again — it would halt
// on the identity check every cycle. Blocking its directory forever is friction
// with no safety behind it.
func TestSessionForADeletedSandboxIsCleared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	store := session.NewStore(filepath.Join(home, ".agyn", "sync"))
	inode, inodeErr := session.RootInode(root)
	if inodeErr != nil {
		t.Skipf("inode unavailable: %v", inodeErr)
	}
	state := &session.State{
		ID: "stale", Name: "notes-gone-otter", LocalRoot: root,
		LocalRootInode: inode, SandboxID: "sandbox-gone", Sandbox: "gone-otter",
		RemoteRoot: "/workspace", Status: session.StatusHalted,
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	// removeSessionsForSandbox is what `sandbox delete` calls.
	if removed := removeSessionsForSandbox("sandbox-gone"); removed != 1 {
		t.Fatalf("expected one session removed, got %d", removed)
	}
	remaining, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("session survived its sandbox: %+v", remaining)
	}

	// And the directory is free again.
	if _, err := checkSyncLocalRoot(context.Background(), nil, root); err != nil {
		t.Fatalf("directory still blocked: %v", err)
	}
}

// A session for a sandbox that still exists is left alone.
func TestSessionForAnotherSandboxSurvives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	store := session.NewStore(filepath.Join(home, ".agyn", "sync"))
	inode, inodeErr := session.RootInode(root)
	if inodeErr != nil {
		t.Skipf("inode unavailable: %v", inodeErr)
	}
	if err := store.Save(&session.State{
		ID: "live", Name: "notes-live-otter", LocalRoot: root,
		LocalRootInode: inode, SandboxID: "sandbox-live", Sandbox: "live-otter",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if removed := removeSessionsForSandbox("sandbox-other"); removed != 0 {
		t.Fatalf("removed a session for a different sandbox: %d", removed)
	}
	if states, _ := store.List(); len(states) != 1 {
		t.Fatalf("expected the session to survive, got %d", len(states))
	}
}
