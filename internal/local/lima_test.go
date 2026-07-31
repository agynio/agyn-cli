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
func TestApplyInstanceSizeRewritesTheInstanceConfig(t *testing.T) {
	path := writeInstanceConfig(t, "cpus: 4\nmemory: 8GiB\nimages:\n  - location: disk.qcow2\n")

	if err := applyInstanceSize(VMOptions{CPUs: 8, Memory: "16GiB"}); err != nil {
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

func TestApplyInstanceSizeLeavesUnsetFieldsAlone(t *testing.T) {
	path := writeInstanceConfig(t, "cpus: 4\nmemory: 8GiB\n")

	if err := applyInstanceSize(VMOptions{CPUs: 8}); err != nil {
		t.Fatalf("apply instance size: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "cpus: 8") || !strings.Contains(got, "memory: 8GiB") {
		t.Fatalf("expected only cpus to change:\n%s", got)
	}
}

// Starting before the instance exists is normal: creation writes the config.
func TestApplyInstanceSizeIgnoresAMissingConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := applyInstanceSize(VMOptions{CPUs: 8, Memory: "16GiB"}); err != nil {
		t.Fatalf("expected a missing config to be ignored, got %v", err)
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
