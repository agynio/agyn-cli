package config

import (
	"os"
	"testing"
)

func TestResolveProfileNamePrecedence(t *testing.T) {
	cfg := &Config{CurrentProfile: "recorded"}

	if got := cfg.ResolveProfileName("flag"); got != "flag" {
		t.Fatalf("expected the flag to win, got %q", got)
	}

	t.Setenv(ProfileEnv, "from-env")
	if got := cfg.ResolveProfileName(""); got != "from-env" {
		t.Fatalf("expected the environment to beat the recorded choice, got %q", got)
	}
	if got := cfg.ResolveProfileName("flag"); got != "flag" {
		t.Fatalf("expected the flag to beat the environment, got %q", got)
	}

	_ = os.Unsetenv(ProfileEnv)
	if got := cfg.ResolveProfileName(""); got != "recorded" {
		t.Fatalf("expected the recorded choice, got %q", got)
	}
	if got := (&Config{}).ResolveProfileName(""); got != DefaultProfileName {
		t.Fatalf("expected the default profile, got %q", got)
	}
}

func TestResolveGatewayURLFallsBackThroughProfileAndLegacy(t *testing.T) {
	cfg := &Config{
		Gateway:  GatewayConfig{URL: "https://legacy.example"},
		Profiles: map[string]Profile{"local": {GatewayURL: "https://local.example"}},
	}

	if got := cfg.ResolveGatewayURLFor("local", ""); got != "https://local.example" {
		t.Fatalf("expected the profile URL, got %q", got)
	}
	// A profile with no URL of its own falls back to the pre-profile setting,
	// so existing configurations keep working.
	if got := cfg.ResolveGatewayURLFor("other", ""); got != "https://legacy.example" {
		t.Fatalf("expected the legacy URL, got %q", got)
	}
	if got := (&Config{}).ResolveGatewayURLFor("any", ""); got != DefaultGatewayURL {
		t.Fatalf("expected the default URL, got %q", got)
	}
	if got := cfg.ResolveGatewayURLFor("local", "https://flag.example"); got != "https://flag.example" {
		t.Fatalf("expected the flag to win, got %q", got)
	}
}

func TestSetProfileKeepsUnsuppliedFields(t *testing.T) {
	cfg := &Config{}
	cfg.SetProfile("local", Profile{GatewayURL: "https://local.example", Organization: "org-1"})
	cfg.SetProfile("local", Profile{Organization: "org-2"})

	got := cfg.Profile("local")
	if got.GatewayURL != "https://local.example" {
		t.Fatalf("expected the gateway URL to survive, got %q", got.GatewayURL)
	}
	if got.Organization != "org-2" {
		t.Fatalf("expected the organization to update, got %q", got.Organization)
	}
}

func TestResolveOrganizationPrefersFlag(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{"local": {Organization: "selected"}}}

	if got := cfg.ResolveOrganization("local", ""); got != "selected" {
		t.Fatalf("expected the selected organization, got %q", got)
	}
	if got := cfg.ResolveOrganization("local", "override"); got != "override" {
		t.Fatalf("expected the flag to override, got %q", got)
	}
}
