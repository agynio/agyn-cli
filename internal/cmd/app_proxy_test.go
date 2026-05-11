package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agynio/agyn-cli/internal/gateway"
)

func TestParseAppProxyArgs(t *testing.T) {
	slug, command, payload, err := parseAppProxyArgs([]string{"reminders", "create-reminder", "--thread", "thread-1", "--delay", "300", "--note", "follow up", "--enabled", "true"})
	if err != nil {
		t.Fatalf("parse app proxy args: %v", err)
	}
	if slug != "reminders" || command != "create-reminder" {
		t.Fatalf("unexpected slug/command: %q %q", slug, command)
	}
	if payload["thread"] != "thread-1" || payload["note"] != "follow up" {
		t.Fatalf("unexpected string payload: %#v", payload)
	}
	if payload["delay"] != int64(300) {
		t.Fatalf("expected numeric delay, got %#v", payload["delay"])
	}
	if payload["enabled"] != true {
		t.Fatalf("expected boolean enabled, got %#v", payload["enabled"])
	}
}

func TestParseAppProxyArgsMissingFlagValueBeforeNextFlag(t *testing.T) {
	_, _, _, err := parseAppProxyArgs([]string{"reminders", "create-reminder", "--thread", "--delay", "300"})
	if err == nil {
		t.Fatalf("expected missing flag value error")
	}
	if err.Error() != "missing value for flag --thread" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppProxyCommandDoesNotSendOrganizationHeader(t *testing.T) {
	t.Setenv(agentIDEnv, "")
	t.Setenv(agynIdentityIDEnv, "")
	var gotOrganizationID string
	var gotAuthorization string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrganizationID = r.Header.Get("x-organization-id")
		gotAuthorization = r.Header.Get("authorization")
		if r.URL.Path != "/apps/reminders/CreateReminder" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reminder-1"}`))
	}))
	defer server.Close()

	cmd := newAppProxyCmd()
	runContext := &RunContext{
		Clients:      gateway.NewClients(server.URL, "token-1"),
		OutputFormat: "json",
	}
	cmd.SetContext(withRunContext(context.Background(), runContext))
	cmd.SetArgs([]string{"reminders", "create-reminder", "--thread", "thread-1", "--delay", "300", "--note", "follow up"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute app proxy command: %v", err)
	}
	if gotOrganizationID != "" {
		t.Fatalf("expected organization header to be omitted, got %q", gotOrganizationID)
	}
	if gotAuthorization != "Bearer token-1" {
		t.Fatalf("expected auth header from client transport, got %q", gotAuthorization)
	}
	if gotPayload["thread"] != "thread-1" || gotPayload["note"] != "follow up" || gotPayload["delay"] != float64(300) {
		t.Fatalf("unexpected payload: %#v", gotPayload)
	}
}
