package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	organizationsv1 "github.com/agynio/agyn-cli/gen/agynio/api/organizations/v1"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/output"
)

type stubOrganizationsGateway struct {
	gatewayv1connect.UnimplementedOrganizationsGatewayHandler
	organizations []*organizationsv1.Organization
	listCalls     int
}

func (s *stubOrganizationsGateway) ListAccessibleOrganizations(context.Context, *connect.Request[organizationsv1.ListAccessibleOrganizationsRequest]) (*connect.Response[organizationsv1.ListAccessibleOrganizationsResponse], error) {
	s.listCalls++
	return connect.NewResponse(&organizationsv1.ListAccessibleOrganizationsResponse{Organizations: s.organizations}), nil
}

func newOrganizationsGatewayTestServer(t *testing.T, service *stubOrganizationsGateway) *httptest.Server {
	t.Helper()
	path, handler := gatewayv1connect.NewOrganizationsGatewayHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	return httptest.NewServer(mux)
}

func TestOrganizationsSelectRequiresATerminal(t *testing.T) {
	withTempHome(t)
	withStdinTerminal(t, false)

	command := newOrganizationsSelectCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	// No gateway client: `select` must give up before the round trip so a
	// script gets the pointer to `use` rather than a prompt nobody answers.
	command.SetContext(withRunContext(context.Background(), &RunContext{
		Config:       &config.Config{},
		ProfileName:  "local",
		OutputFormat: output.FormatTable,
	}))

	err := command.Execute()
	if err == nil {
		t.Fatal("expected select to fail without a terminal")
	}
	if !strings.Contains(err.Error(), "agyn organizations use") {
		t.Fatalf("expected the error to point at `use`, got %v", err)
	}
}

func TestOrganizationsUseWritesTheActiveProfilesOrganization(t *testing.T) {
	withTempHome(t)
	service := &stubOrganizationsGateway{organizations: []*organizationsv1.Organization{
		{Id: "org-acme", Name: "Acme"},
		{Id: "org-globex", Name: "Globex"},
	}}
	server := newOrganizationsGatewayTestServer(t, service)
	defer server.Close()

	cfg := &config.Config{
		CurrentProfile: "local",
		Profiles:       map[string]config.Profile{"local": {}, "staging": {GatewayURL: "https://gateway.staging.example"}},
	}
	command := newOrganizationsUseCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetArgs([]string{"globex"})
	command.SetContext(withRunContext(context.Background(), &RunContext{
		Config:       cfg,
		Clients:      newTestClients(t, server.URL),
		ProfileName:  "staging",
		OutputFormat: output.FormatTable,
	}))

	if err := command.Execute(); err != nil {
		t.Fatalf("organizations use: %v", err)
	}

	stored := reload(t)
	if got := stored.Profile("staging").Organization; got != "org-globex" {
		t.Fatalf("expected the selection on the profile in use, got %q", got)
	}
	if got := stored.Profile("local").Organization; got != "" {
		t.Fatalf("expected other profiles to be untouched, got %q", got)
	}
	// Running under --profile must not also switch the machine to it.
	if stored.CurrentProfile != "local" {
		t.Fatalf("currentProfile = %q", stored.CurrentProfile)
	}
}

func TestMatchOrganizationPrefersIDOverName(t *testing.T) {
	organizations := []*organizationsv1.Organization{
		{Id: "org-acme", Name: "Globex"},
		{Id: "org-globex", Name: "org-acme"},
	}

	matched, err := matchOrganization(organizations, "org-acme")
	if err != nil {
		t.Fatalf("match by ID: %v", err)
	}
	if matched.GetId() != "org-acme" {
		t.Fatalf("expected the ID match to win, got %q", matched.GetId())
	}

	matched, err = matchOrganization(organizations, "globex")
	if err != nil {
		t.Fatalf("match by name: %v", err)
	}
	if matched.GetId() != "org-acme" {
		t.Fatalf("expected a case-insensitive name match, got %q", matched.GetId())
	}
}

func TestMatchOrganizationReportsMissesAndAmbiguity(t *testing.T) {
	organizations := []*organizationsv1.Organization{
		{Id: "org-1", Name: "Acme"},
		{Id: "org-2", Name: "Acme"},
	}

	_, err := matchOrganization(organizations, "Initech")
	if err == nil || !strings.Contains(err.Error(), "org-1") {
		t.Fatalf("expected the candidates to be listed, got %v", err)
	}
	_, err = matchOrganization(organizations, "acme")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected a duplicate name to be ambiguous, got %v", err)
	}
}

func TestResolveOrganizationIDPrecedence(t *testing.T) {
	withTempHome(t)
	service := &stubOrganizationsGateway{organizations: []*organizationsv1.Organization{{Id: "org-sole", Name: "Sole"}}}
	server := newOrganizationsGatewayTestServer(t, service)
	defer server.Close()
	client := gatewayv1connect.NewOrganizationsGatewayClient(newTestClients(t, server.URL).HTTPClient, server.URL)

	selected := &RunContext{
		ProfileName: "local",
		Config:      &config.Config{Profiles: map[string]config.Profile{"local": {Organization: "org-profile"}}},
	}
	got, err := resolveOrganizationID(context.Background(), selected, client, "")
	if err != nil {
		t.Fatalf("resolve from profile: %v", err)
	}
	if got != "org-profile" {
		t.Fatalf("expected the profile selection, got %q", got)
	}
	if service.listCalls != 0 {
		t.Fatal("expected no Gateway call when the profile has a selection")
	}

	got, err = resolveOrganizationID(context.Background(), selected, client, "org-flag")
	if err != nil {
		t.Fatalf("resolve from flag: %v", err)
	}
	if got != "org-flag" {
		t.Fatalf("expected the flag to win, got %q", got)
	}

	unselected := &RunContext{ProfileName: "local", Config: &config.Config{}}
	got, err = resolveOrganizationID(context.Background(), unselected, client, "")
	if err != nil {
		t.Fatalf("resolve sole organization: %v", err)
	}
	if got != "org-sole" {
		t.Fatalf("expected the sole accessible organization, got %q", got)
	}
}

func TestResolveOrganizationIDReportsAmbiguityAndAbsence(t *testing.T) {
	withTempHome(t)
	runContext := &RunContext{ProfileName: "local", Config: &config.Config{}}

	several := &stubOrganizationsGateway{organizations: []*organizationsv1.Organization{
		{Id: "org-1", Name: "Acme"},
		{Id: "org-2", Name: "Globex"},
	}}
	server := newOrganizationsGatewayTestServer(t, several)
	defer server.Close()
	client := gatewayv1connect.NewOrganizationsGatewayClient(newTestClients(t, server.URL).HTTPClient, server.URL)

	_, err := resolveOrganizationID(context.Background(), runContext, client, "")
	if err == nil || !strings.Contains(err.Error(), "agyn organizations select") {
		t.Fatalf("expected ambiguity to point at `select`, got %v", err)
	}

	none := &stubOrganizationsGateway{}
	emptyServer := newOrganizationsGatewayTestServer(t, none)
	defer emptyServer.Close()
	emptyClient := gatewayv1connect.NewOrganizationsGatewayClient(newTestClients(t, emptyServer.URL).HTTPClient, emptyServer.URL)

	if _, err := resolveOrganizationID(context.Background(), runContext, emptyClient, ""); err == nil {
		t.Fatal("expected an error when the caller has no organizations")
	}
}

func withStdinTerminal(t *testing.T, terminal bool) {
	t.Helper()
	previous := stdinIsTerminal
	stdinIsTerminal = func() bool { return terminal }
	t.Cleanup(func() { stdinIsTerminal = previous })
}
