package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/local"
)

func TestWriteLocalProfileAdoptsAnUnchosenProfile(t *testing.T) {
	withTempHome(t)

	provisioned := config.Profile{
		GatewayURL:   "https://gateway.agyn.dev:2496",
		Organization: "org-seeded",
		CAFile:       "~/.agyn/local/certs/agyn-local-ca.pem",
	}
	current, err := writeLocalProfile(config.LocalProfileName, provisioned, "agyn_local_secret")
	if err != nil {
		t.Fatalf("write local profile: %v", err)
	}
	// Nothing had been chosen, so the freshly provisioned VM becomes what
	// commands address — the point of provisioning at all.
	if current != config.LocalProfileName {
		t.Fatalf("current profile = %q", current)
	}

	cfg := reload(t)
	if cfg.CurrentProfile != config.LocalProfileName {
		t.Fatalf("recorded currentProfile = %q", cfg.CurrentProfile)
	}
	if got := cfg.Profile(config.LocalProfileName); got != provisioned {
		t.Fatalf("stored profile = %#v", got)
	}
	if !auth.HasTokenFor(config.LocalProfileName) {
		t.Fatal("expected the bootstrap token to be stored for the profile")
	}
}

func TestWriteLocalProfileKeepsAnExistingSelection(t *testing.T) {
	withTempHome(t)
	if err := config.SaveProfiles("staging", map[string]config.Profile{
		"staging": {GatewayURL: "https://gateway.staging.example", Organization: "org-staging"},
	}); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}

	current, err := writeLocalProfile(config.LocalProfileName, config.Profile{
		GatewayURL:   "https://gateway.agyn.dev:2496",
		Organization: "org-seeded",
	}, "agyn_local_secret")
	if err != nil {
		t.Fatalf("write local profile: %v", err)
	}
	// Booting a VM must not repoint a CLI that is already addressing a cluster.
	if current != "staging" {
		t.Fatalf("current profile = %q", current)
	}

	cfg := reload(t)
	if cfg.CurrentProfile != "staging" {
		t.Fatalf("recorded currentProfile = %q", cfg.CurrentProfile)
	}
	if cfg.Profile("staging").GatewayURL != "https://gateway.staging.example" {
		t.Fatal("expected the existing profile to survive")
	}
	if cfg.Profile(config.LocalProfileName).Organization != "org-seeded" {
		t.Fatal("expected the local profile to be written anyway")
	}
}

func TestWriteLocalProfileTargetsANamedProfile(t *testing.T) {
	withTempHome(t)

	if _, err := writeLocalProfile("sandbox", config.Profile{
		GatewayURL:   "https://gateway.agyn.dev:2500",
		Organization: "org-seeded",
	}, "agyn_local_secret"); err != nil {
		t.Fatalf("write local profile: %v", err)
	}

	cfg := reload(t)
	if _, exists := cfg.Profiles[config.LocalProfileName]; exists {
		t.Fatal("expected --profile to write only the named profile")
	}
	if cfg.Profile("sandbox").GatewayURL != "https://gateway.agyn.dev:2500" {
		t.Fatal("expected the named profile to be written")
	}
	if !auth.HasTokenFor("sandbox") {
		t.Fatal("expected the token to be stored under the named profile")
	}
}

func TestLocalCredentialsNoProfileSkipsProvisioning(t *testing.T) {
	withTempHome(t)

	cmd := newLocalCredentialsCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--no-profile"})
	// Skipping has to happen before anything reaches for the VM: this runs on
	// machines with no VM, and often with no limactl at all.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("local credentials --no-profile: %v", err)
	}
	if !strings.Contains(stdout.String(), "--no-profile") {
		t.Fatalf("expected the skip to be reported, got %q", stdout.String())
	}

	cfg := reload(t)
	if len(cfg.Profiles) != 0 || cfg.CurrentProfile != "" {
		t.Fatalf("expected no configuration to be written, got %#v", cfg)
	}
	if auth.HasTokenFor(config.LocalProfileName) {
		t.Fatal("expected no credential to be stored")
	}
}

func TestLocalProfileFlagsDefaultToTheLocalProfile(t *testing.T) {
	if got := (localProfileFlags{}).targetProfile(); got != config.LocalProfileName {
		t.Fatalf("default target profile = %q", got)
	}
	if got := (localProfileFlags{profile: " sandbox "}).targetProfile(); got != "sandbox" {
		t.Fatalf("named target profile = %q", got)
	}
}

func TestLocalBootstrapTokenReusesOnlyWhatItMinted(t *testing.T) {
	withTempHome(t)

	minted, err := localBootstrapToken(config.LocalProfileName)
	if err != nil {
		t.Fatalf("mint bootstrap token: %v", err)
	}
	if !local.IsBootstrapToken(minted) {
		t.Fatalf("expected a freshly minted token, got %q", minted)
	}

	// Restarting a VM must not invalidate the credential the host already holds.
	if err := auth.SaveTokenFor(config.LocalProfileName, minted); err != nil {
		t.Fatalf("store token: %v", err)
	}
	reused, err := localBootstrapToken(config.LocalProfileName)
	if err != nil {
		t.Fatalf("reuse bootstrap token: %v", err)
	}
	if reused != minted {
		t.Fatalf("expected the stored token to be reused, got %q", reused)
	}

	// A credential stored by `agyn auth set-token` addresses something else; it
	// must never be installed into the VM as its bootstrap token.
	if err := auth.SaveTokenFor(config.LocalProfileName, "a-remote-cluster-api-token"); err != nil {
		t.Fatalf("store foreign token: %v", err)
	}
	replaced, err := localBootstrapToken(config.LocalProfileName)
	if err != nil {
		t.Fatalf("replace foreign token: %v", err)
	}
	if !local.IsBootstrapToken(replaced) || replaced == "a-remote-cluster-api-token" {
		t.Fatalf("expected a freshly minted token, got %q", replaced)
	}
}

func TestForgetLocalProfileRemovesProfileAndToken(t *testing.T) {
	withTempHome(t)
	if err := config.SaveProfiles(config.LocalProfileName, map[string]config.Profile{
		config.LocalProfileName: {GatewayURL: "https://gateway.agyn.dev:2496"},
		"staging":               {GatewayURL: "https://gateway.staging.example"},
	}); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}
	if err := auth.SaveTokenFor(config.LocalProfileName, "agyn_local_secret"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := auth.SaveTokenFor("staging", "agyn_staging_secret"); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	cmd := newLocalDeleteCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := forgetLocalProfile(cmd); err != nil {
		t.Fatalf("forget local profile: %v", err)
	}

	cfg := reload(t)
	if _, exists := cfg.Profiles[config.LocalProfileName]; exists {
		t.Fatal("expected the local profile to be removed by a purge")
	}
	// A dangling selection would leave every later command resolving against a
	// profile the user can no longer see.
	if cfg.CurrentProfile != "" {
		t.Fatalf("expected the selection to be cleared, got %q", cfg.CurrentProfile)
	}
	if auth.HasTokenFor(config.LocalProfileName) {
		t.Fatal("expected the stored token to be removed with the profile")
	}
	if _, exists := cfg.Profiles["staging"]; !exists || !auth.HasTokenFor("staging") {
		t.Fatal("expected other profiles and their tokens to survive")
	}
	if !strings.Contains(stdout.String(), config.LocalProfileName) {
		t.Fatalf("expected the removal to be reported, got %q", stdout.String())
	}
}

func TestForgetLocalProfileIsSilentWhenNothingWasProvisioned(t *testing.T) {
	withTempHome(t)

	cmd := newLocalDeleteCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := forgetLocalProfile(cmd); err != nil {
		t.Fatalf("forget local profile: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("expected nothing to be reported, got %q", stdout.String())
	}
}

func TestShortenHomePathKeepsTheConfigPortable(t *testing.T) {
	home := withTempHome(t)

	caPath := filepath.Join(home, ".agyn", "local", "certs", "agyn-local-ca.pem")
	if got := shortenHomePath(caPath); got != "~/.agyn/local/certs/agyn-local-ca.pem" {
		t.Fatalf("shortened path = %q", got)
	}
	if got := shortenHomePath("/etc/ssl/cert.pem"); got != "/etc/ssl/cert.pem" {
		t.Fatalf("expected a path outside home to be left alone, got %q", got)
	}
}
