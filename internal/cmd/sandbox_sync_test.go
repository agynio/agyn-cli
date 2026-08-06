package cmd

import (
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

	_, err = checkSyncLocalRoot(root)
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
	if _, err := checkSyncLocalRoot(nested); err == nil {
		t.Fatal("a nested directory was accepted")
	}

	// An unrelated directory is fine.
	if _, err := checkSyncLocalRoot(t.TempDir()); err != nil {
		t.Fatalf("an unrelated directory was refused: %v", err)
	}
}
