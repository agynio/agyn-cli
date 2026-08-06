package transfer_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/internal/sync/client"
	"github.com/agynio/agyn-cli/internal/sync/transfer"
	"github.com/agynio/agyn-cli/internal/sync/tree"
)

// buildCLI builds the real binary once per run. The endpoint's contract
// includes claiming stdout away from every other writer, which only holds in a
// real process — an in-process endpoint would never catch a regression there.
func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "agyn")
	build := exec.Command("go", "build", "-o", binary, "./cmd/agyn")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agyn: %v\n%s", err, out)
	}
	return binary
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the module root")
	return ""
}

// serve spawns `agyn sandbox sync serve` and returns a client on its pipes,
// which is exactly how the Terminal Proxy will run it.
func serve(t *testing.T, binary, root string) (*client.Client, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(binary, "sandbox", "sync", "serve", "--root", root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	diagnostics := &bytes.Buffer{}
	cmd.Stderr = diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatalf("start endpoint: %v", err)
	}
	t.Cleanup(func() {
		stdin.Close()
		_ = cmd.Wait()
	})
	return client.New(stdout, stdin), diagnostics
}

func TestCopyIntoAndBackOutOfTheEndpoint(t *testing.T) {
	binary := buildCLI(t)
	remote := t.TempDir()
	local := t.TempDir()

	source := filepath.Join(local, "src")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "report.txt"), []byte("quarterly\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, diagnostics := serve(t, binary, remote)
	if _, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, ""); err != nil {
		t.Fatalf("handshake: %v (stderr: %s)", err, diagnostics)
	}

	report, err := transfer.Push(c, source, "src", true)
	if err != nil {
		t.Fatalf("push: %v (stderr: %s)", err, diagnostics)
	}
	if report.Files != 2 {
		t.Fatalf("expected 2 files pushed, got %d", report.Files)
	}

	got, err := os.ReadFile(filepath.Join(remote, "src", "report.txt"))
	if err != nil || string(got) != "quarterly\n" {
		t.Fatalf("pushed file wrong: %q %v", got, err)
	}
	info, err := os.Stat(filepath.Join(remote, "src", "nested", "run.sh"))
	if err != nil {
		t.Fatalf("stat pushed script: %v", err)
	}
	// Only the executable bit crosses; ownership never does.
	if info.Mode()&0o100 == 0 {
		t.Fatalf("executable bit lost, mode is %s", info.Mode())
	}

	// And back out again, into a fresh local tree.
	back := filepath.Join(local, "back")
	pulled, err := transfer.Pull(c, "src", back, true)
	if err != nil {
		t.Fatalf("pull: %v (stderr: %s)", err, diagnostics)
	}
	if pulled.Files != 2 {
		t.Fatalf("expected 2 files pulled, got %d", pulled.Files)
	}
	roundTripped, err := os.ReadFile(filepath.Join(back, "report.txt"))
	if err != nil || string(roundTripped) != "quarterly\n" {
		t.Fatalf("round-tripped file wrong: %q %v", roundTripped, err)
	}

	// A copy establishes no relationship, so it must leave no marker behind.
	if marker, _ := tree.Marker(remote); marker != "" {
		t.Fatalf("cp planted a marker %q", marker)
	}
}

// The endpoint must guarantee nothing but protocol frames reaches stdout. A
// single stray byte desynchronizes the stream with no way to recover.
func TestEndpointKeepsDiagnosticsOffTheProtocolStream(t *testing.T) {
	binary := buildCLI(t)
	remote := t.TempDir()
	if err := os.WriteFile(filepath.Join(remote, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, diagnostics := serve(t, binary, remote)
	if _, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, ""); err != nil {
		t.Fatalf("handshake: %v (stderr: %s)", err, diagnostics)
	}
	// Many exchanges: anything writing a banner or a warning to stdout would
	// land between frames and break decoding rather than merely look untidy.
	for range 20 {
		if _, err := c.Scan(nil); err != nil {
			t.Fatalf("scan: %v (stderr: %s)", err, diagnostics)
		}
	}
}

func TestPushRefusesADirectoryWithoutRecursive(t *testing.T) {
	binary := buildCLI(t)
	remote := t.TempDir()
	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "tree"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	c, _ := serve(t, binary, remote)
	if _, err := c.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, ""); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	_, err := transfer.Push(c, filepath.Join(local, "tree"), "tree", false)
	if err == nil || !strings.Contains(err.Error(), "use -r") {
		t.Fatalf("expected a directory without -r to be refused, got %v", err)
	}
}
