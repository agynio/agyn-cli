package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A config written before instances existed must keep working untouched: it
// describes somebody's running VM, and mangling it would strand them.
func TestLoadMigratesFlatLocalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `
local:
  port: 2497
  apiPort: 6445
  version: "0.1.0"
  cpus: 6
  memory: 12GiB
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	settings := cfg.InstanceSettings(DefaultInstanceName)
	if settings.Port != 2497 || settings.APIPort != 6445 {
		t.Fatalf("ports not migrated: %+v", settings)
	}
	if settings.Version != "0.1.0" || settings.CPUs != 6 || settings.Memory != "12GiB" {
		t.Fatalf("settings not migrated: %+v", settings)
	}
	if cfg.Local.Port != 0 || cfg.Local.Version != "" {
		t.Fatalf("flat fields should be cleared after migration: %+v", cfg.Local)
	}
	if got := cfg.ResolveInstanceName(""); got != DefaultInstanceName {
		t.Fatalf("unnamed config should resolve to the default instance, got %q", got)
	}
}

// An explicit instances entry is the newer shape and must win, so a config
// carrying both is not silently reverted to the older values.
func TestLoadPrefersInstancesOverFlat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `
local:
  port: 2496
  instances:
    agyn:
      port: 2500
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.InstanceSettings(DefaultInstanceName).Port; got != 2500 {
		t.Fatalf("expected the instances entry to win, got port %d", got)
	}
}

func TestResolveInstanceNamePrefersFlagThenSelection(t *testing.T) {
	cfg := &Config{Local: LocalConfig{Current: "v2"}}
	if got := cfg.ResolveInstanceName("other"); got != "other" {
		t.Fatalf("--instance should win, got %q", got)
	}
	if got := cfg.ResolveInstanceName(""); got != "v2" {
		t.Fatalf("stored selection should be used, got %q", got)
	}
	empty := &Config{}
	if got := empty.ResolveInstanceName(""); got != DefaultInstanceName {
		t.Fatalf("expected the default instance, got %q", got)
	}
}

// Each VM needs its own profile, or a second VM overwrites the first one's
// endpoint and token.
func TestLocalProfileForIsPerInstance(t *testing.T) {
	if got := LocalProfileFor(DefaultInstanceName); got != LocalProfileName {
		t.Fatalf("default instance should keep the bare profile name, got %q", got)
	}
	if got := LocalProfileFor(""); got != LocalProfileName {
		t.Fatalf("unnamed instance should keep the bare profile name, got %q", got)
	}
	if got := LocalProfileFor("v2"); got != "local-v2" {
		t.Fatalf("expected local-v2, got %q", got)
	}
}

// Saving one VM must not disturb another's settings, or reintroduce the old
// flat keys alongside the map.
func TestSaveInstanceKeepsOthersAndDropsFlatKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `
local:
  port: 2496
  instances:
    v2:
      port: 2498
`)

	if err := SaveInstance(DefaultInstanceName, LocalInstance{Port: 2496, APIPort: 6445}); err != nil {
		t.Fatalf("save: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.InstanceSettings("v2").Port; got != 2498 {
		t.Fatalf("other instance disturbed, got port %d", got)
	}
	if got := cfg.InstanceSettings(DefaultInstanceName).Port; got != 2496 {
		t.Fatalf("saved instance wrong, got port %d", got)
	}

	raw, err := os.ReadFile(filepath.Join(home, ConfigDir, ConfigFile))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if contains(string(raw), "\n    port: 2496\n") && !contains(string(raw), "instances") {
		t.Fatalf("flat keys should be gone after a save:\n%s", raw)
	}
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ConfigDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
