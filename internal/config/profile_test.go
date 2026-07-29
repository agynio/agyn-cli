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

func TestResolveGatewayURLFallsBackThroughProfile(t *testing.T) {
	cfg := &Config{
		Profiles: map[string]Profile{"local": {GatewayURL: "https://local.example"}},
	}

	if got := cfg.ResolveGatewayURLFor("local", ""); got != "https://local.example" {
		t.Fatalf("expected the profile URL, got %q", got)
	}
	if got := cfg.ResolveGatewayURLFor("other", ""); got != DefaultGatewayURL {
		t.Fatalf("expected the default URL for an unconfigured profile, got %q", got)
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

func TestResolveOrganizationPrefersEnvironmentOverProfile(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{"local": {Organization: "selected"}}}

	t.Setenv(OrganizationEnv, "from-env")
	if got := cfg.ResolveOrganization("local", ""); got != "from-env" {
		t.Fatalf("expected the environment to beat the profile, got %q", got)
	}
	if got := cfg.ResolveOrganization("local", "from-flag"); got != "from-flag" {
		t.Fatalf("expected the flag to beat the environment, got %q", got)
	}
	_ = os.Unsetenv(OrganizationEnv)
	if got := cfg.ResolveOrganization("local", ""); got != "selected" {
		t.Fatalf("expected the profile selection, got %q", got)
	}
}

func TestRemoveProfileClearsADanglingSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveProfiles("staging", map[string]Profile{
		"local":   {GatewayURL: "https://local.example"},
		"staging": {GatewayURL: "https://staging.example"},
	}); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}

	if err := RemoveProfile("staging"); err != nil {
		t.Fatalf("remove profile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, exists := cfg.Profiles["staging"]; exists {
		t.Fatal("expected the profile to be gone")
	}
	if _, exists := cfg.Profiles["local"]; !exists {
		t.Fatal("expected other profiles to survive")
	}
	if cfg.CurrentProfile != "" {
		t.Fatalf("expected the selection to be cleared, got %q", cfg.CurrentProfile)
	}
}

func TestRemoveProfileKeepsUnrelatedSelectionAndKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveLocal(LocalConfig{Port: 3000}); err != nil {
		t.Fatalf("seed local config: %v", err)
	}
	if err := SaveProfiles("local", map[string]Profile{
		"local":   {GatewayURL: "https://local.example"},
		"staging": {GatewayURL: "https://staging.example"},
	}); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}

	if err := RemoveProfile("staging"); err != nil {
		t.Fatalf("remove profile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.CurrentProfile != "local" {
		t.Fatalf("expected the selection to survive, got %q", cfg.CurrentProfile)
	}
	if cfg.Local.Port != 3000 {
		t.Fatalf("expected unrelated config keys to survive, got %#v", cfg.Local)
	}
}
