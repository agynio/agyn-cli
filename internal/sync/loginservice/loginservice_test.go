package loginservice

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubRun keeps the tests off the developer's launchd and systemd. Writing a
// unit into a temporary HOME is harmless; loading it registers a real agent
// that outlives the test.
func stubRun(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	original := run
	run = func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	t.Cleanup(func() { run = original })
	return &calls
}

// Resume-at-login is opt-in. Nothing may exist until install is called, or a
// reboot silently brings sessions back that the engineer had forgotten about.
func TestNothingIsInstalledUntilAsked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, installed := Status(); installed {
		t.Fatal("a service was reported installed without install being called")
	}
}

func TestInstallIsIdempotentAndUninstallLeavesNothing(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no user-level service manager targeted on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := stubRun(t)

	path, err := Install("/usr/local/bin/agyn")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.HasPrefix(path, home) {
		t.Fatalf("installed outside the user's home: %s", path)
	}
	if _, installed := Status(); !installed {
		t.Fatal("status did not see the installation")
	}

	// Installing twice must leave one registration, not two.
	if _, err := Install("/usr/local/bin/agyn"); err != nil {
		t.Fatalf("second install: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one registration, found %d", len(entries))
	}

	if _, err := Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, installed := Status(); installed {
		t.Fatal("uninstall left the service registered")
	}
	// Uninstalling what is not there is not an error: the command's job is to
	// leave nothing installed.
	if _, err := Uninstall(); err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	if len(*calls) == 0 {
		t.Fatal("the service manager was never asked to register anything")
	}
}

// The service runs exactly one command. A login item that could be pointed at
// anything else would be a way to run arbitrary things at login.
func TestServiceRunsOnlyResumeAll(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no user-level service manager targeted on this platform")
	}
	t.Setenv("HOME", t.TempDir())
	stubRun(t)
	path, err := Install("/usr/local/bin/agyn")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	unit := string(body)
	for _, fragment := range []string{"sandbox", "sync", "resume-all"} {
		if !strings.Contains(unit, fragment) {
			t.Fatalf("unit does not run resume-all: %s", unit)
		}
	}
	if strings.Contains(unit, "connect") || strings.Contains(unit, "delete") {
		t.Fatalf("unit runs something other than resume-all: %s", unit)
	}
}
