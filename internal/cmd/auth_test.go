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
	usersv1 "github.com/agynio/agyn-cli/gen/agynio/api/users/v1"
	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/output"
)

type stubUsersGateway struct {
	gatewayv1connect.UnimplementedUsersGatewayHandler
	user *usersv1.User
}

func (s *stubUsersGateway) GetMe(context.Context, *connect.Request[usersv1.GetMeRequest]) (*connect.Response[usersv1.GetMeResponse], error) {
	return connect.NewResponse(&usersv1.GetMeResponse{
		User:        s.user,
		ClusterRole: usersv1.ClusterRole_CLUSTER_ROLE_ADMIN,
	}), nil
}

func TestAuthSetTokenReadsStdinNotArgv(t *testing.T) {
	withTempHome(t)
	withStdinTerminal(t, false)

	command := newAuthSetTokenCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetIn(strings.NewReader("  agyn_piped_secret\n"))
	command.SetArgs(nil)
	command.SetContext(withRunContext(context.Background(), &RunContext{
		Config:       &config.Config{},
		ProfileName:  "staging",
		OutputFormat: output.FormatTable,
	}))

	if err := command.Execute(); err != nil {
		t.Fatalf("auth set-token: %v", err)
	}

	stored, err := auth.LoadTokenFor("staging", auth.TokenOptions{})
	if err != nil {
		t.Fatalf("load stored token: %v", err)
	}
	if stored != "agyn_piped_secret" {
		t.Fatalf("stored token = %q", stored)
	}
	// The token must never be a flag value: that would put it in shell history
	// and the process table.
	if command.Flags().Lookup("token") != nil {
		t.Fatal("set-token must not accept the token as a flag")
	}
}

func TestAuthSetTokenRejectsEmptyInput(t *testing.T) {
	withTempHome(t)
	withStdinTerminal(t, false)

	command := newAuthSetTokenCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetIn(strings.NewReader("\n"))
	command.SetArgs(nil)
	command.SetContext(withRunContext(context.Background(), &RunContext{
		Config:       &config.Config{},
		ProfileName:  "local",
		OutputFormat: output.FormatTable,
	}))

	if err := command.Execute(); err == nil {
		t.Fatal("expected empty input to be rejected")
	}
	if auth.HasTokenFor("local") {
		t.Fatal("expected nothing to be stored")
	}
}

func TestAuthWhoamiReportsProfileIdentityAndOrganization(t *testing.T) {
	withTempHome(t)
	service := &stubUsersGateway{user: &usersv1.User{
		Meta:     &usersv1.EntityMeta{Id: "user-1"},
		Username: "ada",
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
	}}
	path, handler := gatewayv1connect.NewUsersGatewayHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	command := newAuthWhoamiCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetArgs(nil)
	command.SetContext(withRunContext(context.Background(), &RunContext{
		Config:       &config.Config{Profiles: map[string]config.Profile{"staging": {Organization: "org-staging"}}},
		Clients:      newTestClients(t, server.URL),
		ProfileName:  "staging",
		OutputFormat: output.FormatTable,
	}))

	stdout := captureStdout(t, func() {
		if err := command.Execute(); err != nil {
			t.Fatalf("auth whoami: %v", err)
		}
	})

	for _, want := range []string{"staging", "user-1", "ada", "ada@example.com", "admin", "org-staging"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output:\n%s", want, stdout)
		}
	}
}
