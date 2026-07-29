package auth

import (
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

func TestRemoveLastTokenLeavesNoCredential(t *testing.T) {
	withHome(t)
	if err := SaveTokenFor("local", "token-local"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := RemoveTokenFor("local"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// An emptied file is still the profile format, not a bare token — reading
	// it as one would report a credential for every profile.
	if HasTokenFor("local") || HasTokenFor("staging") {
		t.Fatal("expected no profile to have a token")
	}
	if _, err := LoadTokenFor("local", TokenOptions{}); err == nil {
		t.Fatal("expected an error for a profile with no token")
	}
}
