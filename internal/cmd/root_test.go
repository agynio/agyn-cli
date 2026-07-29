package cmd

import (
	"strings"
	"testing"

	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/gateway"
	"github.com/spf13/cobra"
)

// newTestClients points the shared gateway client at a test server, with no
// extra trust anchors — the server is plain HTTP.
func newTestClients(t *testing.T, baseURL string) *gateway.Clients {
	t.Helper()
	clients, err := gateway.NewClients(baseURL, "token-1", gateway.Options{})
	if err != nil {
		t.Fatalf("build gateway clients: %v", err)
	}
	return clients
}

func TestResolveProfileNameRejectsUnconfiguredName(t *testing.T) {
	t.Setenv(config.ProfileEnv, "")
	cfg := &config.Config{Profiles: map[string]config.Profile{
		"local":   {GatewayURL: "https://local.example"},
		"staging": {GatewayURL: "https://staging.example"},
	}}

	_, err := resolveProfileName(cfg, "stagign")
	if err == nil {
		t.Fatal("expected a typo to be rejected")
	}
	if !strings.Contains(err.Error(), "local, staging") {
		t.Fatalf("expected the available profiles to be listed, got %v", err)
	}

	name, err := resolveProfileName(cfg, "staging")
	if err != nil {
		t.Fatalf("resolve configured profile: %v", err)
	}
	if name != "staging" {
		t.Fatalf("expected staging, got %q", name)
	}
}

func TestResolveProfileNameAllowsUnconfiguredDefault(t *testing.T) {
	t.Setenv(config.ProfileEnv, "")
	// Nothing named it, so the built-in default is not a typo: a machine with
	// no config file must still be able to run commands.
	name, err := resolveProfileName(&config.Config{}, "")
	if err != nil {
		t.Fatalf("resolve implicit default: %v", err)
	}
	if name != config.DefaultProfileName {
		t.Fatalf("expected %q, got %q", config.DefaultProfileName, name)
	}
}

func TestResolveProfileNameRejectsUnconfiguredEnvName(t *testing.T) {
	t.Setenv(config.ProfileEnv, "ghost")
	if _, err := resolveProfileName(&config.Config{}, ""); err == nil {
		t.Fatal("expected AGYN_PROFILE naming an unconfigured profile to be rejected")
	}
}

func TestRunContextOrganizationIDPrecedence(t *testing.T) {
	t.Setenv(config.OrganizationEnv, "")
	runContext := &RunContext{
		ProfileName: "local",
		Config:      &config.Config{Profiles: map[string]config.Profile{"local": {Organization: "from-profile"}}},
	}

	if got := runContext.OrganizationID(""); got != "from-profile" {
		t.Fatalf("expected the profile selection, got %q", got)
	}
	if got := runContext.OrganizationID("from-flag"); got != "from-flag" {
		t.Fatalf("expected the flag to win, got %q", got)
	}

	t.Setenv(config.OrganizationEnv, "from-env")
	if got := runContext.OrganizationID(""); got != "from-env" {
		t.Fatalf("expected the environment to beat the profile, got %q", got)
	}
	if got := runContext.OrganizationID("from-flag"); got != "from-flag" {
		t.Fatalf("expected the flag to beat the environment, got %q", got)
	}

	// Nothing selected: the caller is expected to fall back to the Gateway.
	empty := &RunContext{ProfileName: "local", Config: &config.Config{}}
	t.Setenv(config.OrganizationEnv, "")
	if got := empty.OrganizationID(""); got != "" {
		t.Fatalf("expected no organization, got %q", got)
	}
}

func TestAllowMissingTokenRequiresAgentID(t *testing.T) {
	t.Setenv(agentIDEnv, "")
	t.Setenv(agynIdentityIDEnv, "")
	command := buildCommand("threads")
	if allowMissingToken(command) {
		t.Fatal("expected false when AGENT_ID is not set")
	}
}

func TestAllowMissingTokenThreadsCommand(t *testing.T) {
	t.Setenv(agynIdentityIDEnv, "")
	t.Setenv(agentIDEnv, "agent-123")
	command := buildCommand("threads", "send")
	if !allowMissingToken(command) {
		t.Fatal("expected true for threads command when AGENT_ID is set")
	}
}

func TestAllowMissingTokenNonThreadsCommand(t *testing.T) {
	t.Setenv(agynIdentityIDEnv, "")
	t.Setenv(agentIDEnv, "agent-123")
	command := buildCommand("apps")
	if allowMissingToken(command) {
		t.Fatal("expected false for non-threads command")
	}
}

func buildCommand(parts ...string) *cobra.Command {
	root := &cobra.Command{Use: "agyn"}
	current := root
	for _, part := range parts {
		child := &cobra.Command{Use: part}
		current.AddCommand(child)
		current = child
	}
	return current
}
