package cmd

import (
	"testing"
	"time"

	"github.com/agynio/agyn-cli/internal/config"
)

// Thirty minutes suits an engineer stepping away from a shell and suits nothing
// else, so the value resolves flag -> profile -> whatever the server defaults
// to. Sending nothing is what leaves the last of those to the organization.
func TestResolveSandboxIdleTimeoutPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		profile config.Profile
		want    string // "" means nothing is sent
	}{
		{name: "flag wins over profile", flag: "4h", profile: config.Profile{SandboxIdleTimeout: "2h"}, want: "4h"},
		{name: "profile applies without a flag", profile: config.Profile{SandboxIdleTimeout: "2h"}, want: "2h"},
		{name: "neither leaves it to the server", want: ""},
		{name: "blank flag is not a value", flag: "   ", profile: config.Profile{SandboxIdleTimeout: "2h"}, want: "2h"},
		{name: "blank profile is not a value", profile: config.Profile{SandboxIdleTimeout: "  "}, want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := resolveSandboxIdleTimeout(testCase.flag, testCase.profile)
			if testCase.want == "" {
				if got != nil {
					t.Fatalf("expected nothing to be sent, got %q", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %q, got nothing", testCase.want)
			}
			if *got != testCase.want {
				t.Fatalf("expected %q, got %q", testCase.want, *got)
			}
		})
	}
}

// Two bounds govern a sandbox's life. The stored TTL only says what it was at
// creation; what is left is the number that matters at a shell.
func TestRemainingTTL(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		createdAt time.Time
		ttl       string
		want      string
	}{
		{name: "expired", createdAt: now.Add(-80 * time.Hour), ttl: "72h", want: "expired"},
		{name: "unparseable reports nothing", createdAt: now, ttl: "whenever", want: ""},
		{name: "absent reports nothing", createdAt: now, ttl: "", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := remainingTTL(testCase.createdAt, testCase.ttl); got != testCase.want {
				t.Fatalf("expected %q, got %q", testCase.want, got)
			}
		})
	}

	if got := remainingTTL(now.Add(-time.Hour), "72h"); got == "" || got == "expired" {
		t.Fatalf("expected a live sandbox to report time left, got %q", got)
	}
}
