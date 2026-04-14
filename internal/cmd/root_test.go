package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestAllowMissingTokenRequiresAgentID(t *testing.T) {
	command := buildCommand("threads")
	if allowMissingToken(command) {
		t.Fatal("expected false when AGENT_ID is not set")
	}
}

func TestAllowMissingTokenThreadsCommand(t *testing.T) {
	t.Setenv(agentIDEnv, "agent-123")
	command := buildCommand("threads", "send")
	if !allowMissingToken(command) {
		t.Fatal("expected true for threads command when AGENT_ID is set")
	}
}

func TestAllowMissingTokenNonThreadsCommand(t *testing.T) {
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
