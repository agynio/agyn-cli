package cmd

import (
	"testing"

	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	llmv1 "github.com/agynio/agyn-cli/gen/agynio/api/llm/v1"
)

func TestSubscriptionsCommandRegistration(t *testing.T) {
	cmd := newSubscriptionsCmd()
	if cmd.Use != "subscriptions" {
		t.Fatalf("unexpected command use %q", cmd.Use)
	}
	for _, name := range []string{"create", "list", "show", "update", "delete", "attach", "detach", "attachments"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s command: %v", name, err)
		}
		if found == nil || found.Name() != name {
			t.Fatalf("missing %s command", name)
		}
	}
}

func TestParseVendor(t *testing.T) {
	cases := []struct {
		raw     string
		want    llmv1.Vendor
		wantErr bool
	}{
		{"anthropic", llmv1.Vendor_VENDOR_ANTHROPIC, false},
		{"OPENAI", llmv1.Vendor_VENDOR_OPENAI, false},
		{"  anthropic  ", llmv1.Vendor_VENDOR_ANTHROPIC, false},
		{"claude", llmv1.Vendor_VENDOR_UNSPECIFIED, true},
		{"gemini", llmv1.Vendor_VENDOR_UNSPECIFIED, true},
		{"", llmv1.Vendor_VENDOR_UNSPECIFIED, true},
	}
	for _, tc := range cases {
		got, err := parseVendor(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseVendor(%q) = %v, want an error", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseVendor(%q): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("parseVendor(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// Environment scope lives on `agyn environments subscriptions`, where the
// environment is named rather than typed as a UUID; this group covers agent
// scope, which has no environment to hang off.
func TestSubscriptionsAttachRequiresAnAgent(t *testing.T) {
	for _, name := range []string{"attach", "detach"} {
		t.Run(name, func(t *testing.T) {
			cmd := newSubscriptionsCmd()
			cmd.SetArgs([]string{name, "some-subscription"})
			cmd.SetOut(discardWriter{})
			cmd.SetErr(discardWriter{})
			if err := cmd.Execute(); err == nil {
				t.Fatal("expected an error naming --agent")
			}
		})
	}
}

func TestEnvironmentSubscriptionsCommandRegistration(t *testing.T) {
	cmd := newEnvironmentSubscriptionsCmd()
	if cmd.Use != "subscriptions" {
		t.Fatalf("unexpected command use %q", cmd.Use)
	}
	for _, name := range []string{"list", "attach", "detach"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s command: %v", name, err)
		}
		if found == nil || found.Name() != name {
			t.Fatalf("missing %s command", name)
		}
	}
}

func TestAttachmentTargetNamesTheScope(t *testing.T) {
	agent := subscriptionAttachmentOutput{AgentID: "a-1"}
	if got := attachmentTarget(agent); got != "agent a-1" {
		t.Fatalf("attachmentTarget(agent) = %q", got)
	}
	environment := subscriptionAttachmentOutput{EnvironmentID: "e-1"}
	if got := attachmentTarget(environment); got != "environment e-1" {
		t.Fatalf("attachmentTarget(environment) = %q", got)
	}
}

func TestParseLLMMode(t *testing.T) {
	for raw, want := range map[string]agentsv1.LLMMode{
		"platform": agentsv1.LLMMode_LLM_MODE_PLATFORM,
		"native":   agentsv1.LLMMode_LLM_MODE_NATIVE,
		"NATIVE":   agentsv1.LLMMode_LLM_MODE_NATIVE,
	} {
		got, err := parseLLMMode(raw)
		if err != nil {
			t.Fatalf("parseLLMMode(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseLLMMode(%q) = %v, want %v", raw, got, want)
		}
	}
	if _, err := parseLLMMode("hosted"); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
}

// An environment that predates native mode carries no mode at all, and reading
// that as anything but platform would report a change nobody made.
func TestLLMModeLabelDefaultsToPlatform(t *testing.T) {
	if got := llmModeLabel(agentsv1.LLMMode_LLM_MODE_UNSPECIFIED); got != "platform" {
		t.Fatalf("llmModeLabel(unspecified) = %q", got)
	}
	if got := llmModeLabel(agentsv1.LLMMode_LLM_MODE_NATIVE); got != "native" {
		t.Fatalf("llmModeLabel(native) = %q", got)
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
