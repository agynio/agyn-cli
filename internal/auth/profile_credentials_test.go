package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestSaveAndLoadTokenPerProfile(t *testing.T) {
	withHome(t)

	if err := SaveTokenFor("local", "token-local"); err != nil {
		t.Fatalf("save local: %v", err)
	}
	if err := SaveTokenFor("staging", "token-staging"); err != nil {
		t.Fatalf("save staging: %v", err)
	}

	// Writing one profile must not disturb another.
	for profile, want := range map[string]string{"local": "token-local", "staging": "token-staging"} {
		got, err := LoadTokenFor(profile, TokenOptions{})
		if err != nil {
			t.Fatalf("load %s: %v", profile, err)
		}
		if got != want {
			t.Fatalf("profile %s: expected %q, got %q", profile, want, got)
		}
	}
}

func TestLoadTokenReadsPreProfileFile(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".agyn")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("bare-token\n"), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	// A file written before profiles existed still authenticates.
	got, err := LoadTokenFor("local", TokenOptions{})
	if err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	if got != "bare-token" {
		t.Fatalf("expected the legacy token, got %q", got)
	}
}

func TestSaveTokenMigratesPreProfileFile(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".agyn")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("bare-token\n"), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	if err := SaveTokenFor("staging", "token-staging"); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The legacy token is carried onto the profile being written, so the
	// machine does not silently lose its only credential.
	local, err := LoadTokenFor("staging", TokenOptions{})
	if err != nil || local != "token-staging" {
		t.Fatalf("expected the new token, got %q (%v)", local, err)
	}
}

func TestLoadTokenMissingProfileReportsProfile(t *testing.T) {
	withHome(t)
	if err := SaveTokenFor("local", "token-local"); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err := LoadTokenFor("staging", TokenOptions{})
	if err == nil {
		t.Fatal("expected an error for a profile with no token")
	}

	got, err := LoadTokenFor("staging", TokenOptions{AllowMissing: true})
	if err != nil || got != "" {
		t.Fatalf("expected an empty token when missing is allowed, got %q (%v)", got, err)
	}
}

func TestRemoveTokenLeavesOtherProfiles(t *testing.T) {
	withHome(t)
	if err := SaveTokenFor("local", "token-local"); err != nil {
		t.Fatalf("save local: %v", err)
	}
	if err := SaveTokenFor("staging", "token-staging"); err != nil {
		t.Fatalf("save staging: %v", err)
	}

	if err := RemoveTokenFor("local"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if HasTokenFor("local") {
		t.Fatal("expected the local token to be gone")
	}
	if !HasTokenFor("staging") {
		t.Fatal("expected the staging token to survive")
	}
}
