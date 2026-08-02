package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lima reads cpus and memory from the instance's own config, which only
// creation writes. Without applying them on start, `agyn local config set`
// stored a size that silently never took effect however often the VM was
// restarted.
func TestApplyInstanceSettingsRewritesTheInstanceConfig(t *testing.T) {
	path := writeInstanceConfig(t, "cpus: 4\nmemory: 8GiB\nimages:\n  - location: disk.qcow2\n")

	if err := applyInstanceSettings(VMOptions{CPUs: 8, Memory: "16GiB"}); err != nil {
		t.Fatalf("apply instance size: %v", err)
	}

	got := readFile(t, path)
	for _, want := range []string{"cpus: 8", "memory: 16GiB"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in:\n%s", want, got)
		}
	}
	// Everything else is Lima's, including the disk the VM boots from.
	if !strings.Contains(got, "location: disk.qcow2") {
		t.Fatalf("expected the rest of the config to survive:\n%s", got)
	}
}

func TestApplyInstanceSettingsLeavesUnsetFieldsAlone(t *testing.T) {
	path := writeInstanceConfig(t, "cpus: 4\nmemory: 8GiB\n")

	if err := applyInstanceSettings(VMOptions{CPUs: 8}); err != nil {
		t.Fatalf("apply instance size: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "cpus: 8") || !strings.Contains(got, "memory: 8GiB") {
		t.Fatalf("expected only cpus to change:\n%s", got)
	}
}

// Starting before the instance exists is normal: creation writes the config.
func TestApplyInstanceSettingsIgnoresAMissingConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := applyInstanceSettings(VMOptions{CPUs: 8, Memory: "16GiB"}); err != nil {
		t.Fatalf("expected a missing config to be ignored, got %v", err)
	}
}

// The forwarded port has exactly the same problem as the size: creation writes
// it, so `agyn local config set port` stored a port the VM never listened on
// and the host kept answering on the old one.
func TestApplyInstanceSettingsRewritesTheForwardedPorts(t *testing.T) {
	path := writeInstanceConfig(t, `portForwards:
  - guestPort: 6443
    hostIP: "127.0.0.1"
    hostPort: 6445
  - guestPort: 32443
    hostIP: "127.0.0.1"
    hostPort: 2497
`)

	if err := applyInstanceSettings(VMOptions{Port: 2496, APIPort: 6446}); err != nil {
		t.Fatalf("apply instance settings: %v", err)
	}

	got := readFile(t, path)
	for _, want := range []string{"hostPort: 2496", "hostPort: 6446"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in:\n%s", want, got)
		}
	}
	// The guest ports name the forward; rewriting them would break it.
	for _, want := range []string{"guestPort: 6443", "guestPort: 32443"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to survive in:\n%s", want, got)
		}
	}
}

// Changing the ingress port must not disturb the API forward, which is the
// entry immediately above it and identical in shape.
func TestApplyInstanceSettingsRewritesOnlyTheNamedForward(t *testing.T) {
	path := writeInstanceConfig(t, `portForwards:
  - guestPort: 6443
    hostIP: "127.0.0.1"
    hostPort: 6445
  - guestPort: 32443
    hostIP: "127.0.0.1"
    hostPort: 2497
`)

	if err := applyInstanceSettings(VMOptions{Port: 2496}); err != nil {
		t.Fatalf("apply instance settings: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "hostPort: 6445") {
		t.Fatalf("expected the API forward to be untouched:\n%s", got)
	}
	if strings.Contains(got, "hostPort: 2497") {
		t.Fatalf("expected the ingress forward to change:\n%s", got)
	}
}

func writeInstanceConfig(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".agyn", "local", "lima", InstanceName())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create instance dir: %v", err)
	}
	path := filepath.Join(dir, "lima.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write instance config: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
