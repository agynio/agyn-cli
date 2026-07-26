package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

func TestProfileSetCreatesThenUpdatesWithoutClearing(t *testing.T) {
	withTempHome(t)

	if _, err := runProfileCmd(t, newProfileSetCmd(), &config.Config{}, "default", output.FormatTable,
		"staging", "--gateway-url", "https://gateway.staging.example", "--ca-file", "~/certs/ca.crt"); err != nil {
		t.Fatalf("profile set: %v", err)
	}

	cfg := reload(t)
	if got := cfg.Profile("staging").GatewayURL; got != "https://gateway.staging.example" {
		t.Fatalf("gateway URL = %q", got)
	}

	// A second `set` touching one field must leave the others alone, so a
	// profile can be built up one setting at a time.
	if _, err := runProfileCmd(t, newProfileSetCmd(), cfg, "default", output.FormatTable,
		"staging", "--organization", "org-staging"); err != nil {
		t.Fatalf("profile set organization: %v", err)
	}

	cfg = reload(t)
	profile := cfg.Profile("staging")
	if profile.GatewayURL != "https://gateway.staging.example" {
		t.Fatalf("expected the gateway URL to survive, got %q", profile.GatewayURL)
	}
	if profile.Organization != "org-staging" {
		t.Fatalf("organization = %q", profile.Organization)
	}
	if profile.CAFile != "~/certs/ca.crt" {
		t.Fatalf("expected the CA file to survive, got %q", profile.CAFile)
	}
}

func TestProfileSetRequiresSomethingToSet(t *testing.T) {
	withTempHome(t)

	_, err := runProfileCmd(t, newProfileSetCmd(), &config.Config{}, "default", output.FormatTable, "staging")
	if err == nil {
		t.Fatal("expected a bare `profile set` to be rejected")
	}
	if !strings.Contains(err.Error(), "--gateway-url") {
		t.Fatalf("expected the error to name the flags, got %v", err)
	}
}

func TestProfileUseRecordsTheChoiceAndRejectsUnknownNames(t *testing.T) {
	withTempHome(t)
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"local":   {GatewayURL: "https://gateway.agyn.dev:2496"},
		"staging": {GatewayURL: "https://gateway.staging.example"},
	}}

	if _, err := runProfileCmd(t, newProfileUseCmd(), cfg, "local", output.FormatTable, "staging"); err != nil {
		t.Fatalf("profile use: %v", err)
	}
	if got := reload(t).CurrentProfile; got != "staging" {
		t.Fatalf("currentProfile = %q", got)
	}

	_, err := runProfileCmd(t, newProfileUseCmd(), cfg, "local", output.FormatTable, "production")
	if err == nil {
		t.Fatal("expected an unknown profile to be rejected")
	}
	if !strings.Contains(err.Error(), "local, staging") {
		t.Fatalf("expected the available profiles to be listed, got %v", err)
	}
}

func TestProfileRemoveDeletesProfileTokenAndSelection(t *testing.T) {
	withTempHome(t)
	if err := config.SaveProfiles("staging", map[string]config.Profile{
		"local":   {GatewayURL: "https://gateway.agyn.dev:2496"},
		"staging": {GatewayURL: "https://gateway.staging.example"},
	}); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}
	if err := auth.SaveTokenFor("staging", "agyn_staging_secret"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := auth.SaveTokenFor("local", "agyn_local_secret"); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if _, err := runProfileCmd(t, newProfileRemoveCmd(), reload(t), "staging", output.FormatTable, "staging"); err != nil {
		t.Fatalf("profile remove: %v", err)
	}

	cfg := reload(t)
	if _, exists := cfg.Profiles["staging"]; exists {
		t.Fatal("expected the profile to be gone")
	}
	if _, exists := cfg.Profiles["local"]; !exists {
		t.Fatal("expected other profiles to survive")
	}
	// A dangling currentProfile would make every later command resolve against
	// a profile the user can no longer see.
	if cfg.CurrentProfile != "" {
		t.Fatalf("expected the selection to be cleared, got %q", cfg.CurrentProfile)
	}
	if auth.HasTokenFor("staging") {
		t.Fatal("expected the stored token to be removed with the profile")
	}
	if !auth.HasTokenFor("local") {
		t.Fatal("expected other profiles' tokens to survive")
	}
}

func TestProfileListMarksCurrentAndNeverPrintsTheToken(t *testing.T) {
	withTempHome(t)
	if err := auth.SaveTokenFor("local", "agyn_local_secret"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	cfg := &config.Config{
		CurrentProfile: "local",
		Profiles: map[string]config.Profile{
			"local":   {GatewayURL: "https://gateway.agyn.dev:2496", Organization: "org-local"},
			"staging": {GatewayURL: "https://gateway.staging.example"},
		},
	}

	stdout, err := runProfileCmd(t, newProfileListCmd(), cfg, "local", output.FormatTable)
	if err != nil {
		t.Fatalf("profile list: %v", err)
	}
	if strings.Contains(stdout, "agyn_local_secret") {
		t.Fatal("the token must never be printed")
	}
	local := lineContaining(t, stdout, "local")
	if !strings.HasPrefix(local, "*") {
		t.Fatalf("expected the current profile to be marked, got %q", local)
	}
	if !strings.Contains(local, "org-local") || !strings.HasSuffix(local, "yes") {
		t.Fatalf("expected organization and token state, got %q", local)
	}
	staging := lineContaining(t, stdout, "staging")
	if strings.HasPrefix(staging, "*") || !strings.HasSuffix(staging, "no") {
		t.Fatalf("expected staging to be unmarked with no token, got %q", staging)
	}
}

func TestProfileShowDefaultsToTheActiveProfile(t *testing.T) {
	withTempHome(t)
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"local":   {GatewayURL: "https://gateway.agyn.dev:2496"},
		"staging": {GatewayURL: "https://gateway.staging.example", Organization: "org-staging"},
	}}

	stdout, err := runProfileCmd(t, newProfileShowCmd(), cfg, "staging", output.FormatTable)
	if err != nil {
		t.Fatalf("profile show: %v", err)
	}
	if !strings.Contains(stdout, "https://gateway.staging.example") || !strings.Contains(stdout, "org-staging") {
		t.Fatalf("expected the active profile's resolved settings, got %q", stdout)
	}

	stdout, err = runProfileCmd(t, newProfileShowCmd(), cfg, "staging", output.FormatTable, "local")
	if err != nil {
		t.Fatalf("profile show local: %v", err)
	}
	if !strings.Contains(stdout, "https://gateway.agyn.dev:2496") {
		t.Fatalf("expected the named profile, got %q", stdout)
	}

	if _, err := runProfileCmd(t, newProfileShowCmd(), cfg, "staging", output.FormatTable, "production"); err == nil {
		t.Fatal("expected an unknown profile to be rejected")
	}
}

func TestProfileOutputForFallsBackToTheDefaultGateway(t *testing.T) {
	withTempHome(t)
	// A machine that has never written configuration still resolves an
	// endpoint, so `profile show` has something to report.
	out := profileOutputFor(&config.Config{}, config.DefaultProfileName, config.DefaultProfileName)
	if out.GatewayURL != config.DefaultGatewayURL {
		t.Fatalf("gateway URL = %q", out.GatewayURL)
	}
	if !out.Current || out.TokenStored {
		t.Fatalf("unexpected profile output %#v", out)
	}
}

func runProfileCmd(t *testing.T, command *cobra.Command, cfg *config.Config, profileName string, format output.Format, args ...string) (string, error) {
	t.Helper()
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs(args)
	command.SetContext(withRunContext(context.Background(), &RunContext{
		Config:       cfg,
		ProfileName:  profileName,
		OutputFormat: format,
	}))

	var err error
	stdout := captureStdout(t, func() { err = command.Execute() })
	return stdout, err
}

// captureStdout collects what the output package writes, which goes to
// os.Stdout rather than the command's writer.
func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = saved }()

	run()

	if err := writer.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read captured output: %v", err)
	}
	return string(data)
}

// withTempHome isolates a test from the developer's own configuration and
// returns the home directory it substituted.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.ProfileEnv, "")
	t.Setenv(config.OrganizationEnv, "")
	t.Setenv(config.GatewayURLEnv, "")
	t.Setenv(config.GatewayAddressEnv, "")
	return home
}

func reload(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func lineContaining(t *testing.T, text, needle string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, text)
	return ""
}
