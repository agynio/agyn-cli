package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	egressv1 "github.com/agynio/agyn-cli/gen/agynio/api/egress/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type recordingEgressRulesGateway struct {
	gatewayv1connect.UnimplementedEgressRulesGatewayHandler
	current       *egressv1.EgressRule
	updateRequest *egressv1.UpdateEgressRuleRequest
}

func (s *recordingEgressRulesGateway) GetEgressRule(context.Context, *connect.Request[egressv1.GetEgressRuleRequest]) (*connect.Response[egressv1.GetEgressRuleResponse], error) {
	return connect.NewResponse(&egressv1.GetEgressRuleResponse{EgressRule: s.current}), nil
}

func (s *recordingEgressRulesGateway) UpdateEgressRule(_ context.Context, req *connect.Request[egressv1.UpdateEgressRuleRequest]) (*connect.Response[egressv1.UpdateEgressRuleResponse], error) {
	s.updateRequest = req.Msg
	updated := &egressv1.EgressRule{
		Meta:           s.current.GetMeta(),
		OrganizationId: s.current.GetOrganizationId(),
		Name:           s.current.GetName(),
		Description:    s.current.GetDescription(),
		Matcher:        s.current.GetMatcher(),
		Effect:         s.current.GetEffect(),
	}
	if req.Msg.GetMatcher() != nil {
		updated.Matcher = req.Msg.GetMatcher()
	}
	if req.Msg.GetEffect() != nil {
		updated.Effect = req.Msg.GetEffect()
	}
	return connect.NewResponse(&egressv1.UpdateEgressRuleResponse{EgressRule: updated}), nil
}

func newEgressRulesGatewayTestServer(t *testing.T, service *recordingEgressRulesGateway) *httptest.Server {
	t.Helper()
	path, handler := gatewayv1connect.NewEgressRulesGatewayHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	return httptest.NewServer(mux)
}

func TestParseEgressAction(t *testing.T) {
	action, err := parseEgressAction(" deny ")
	if err != nil {
		t.Fatalf("parse deny action: %v", err)
	}
	if action != egressv1.EgressRuleAction_EGRESS_RULE_ACTION_DENY {
		t.Fatalf("unexpected action %s", action)
	}
	if _, err := parseEgressAction("block"); err == nil {
		t.Fatalf("expected invalid action error")
	}
}

func TestParseEgressHeaders(t *testing.T) {
	headers, err := parseEgressHeaders([]string{
		"X-Static=value",
		"Authorization=bearer-secret:secret-id",
		"Proxy-Authorization=basic-secret:x-access-token:basic-secret-id",
		"X-Basic=basic:user:pass",
	})
	if err != nil {
		t.Fatalf("parse headers: %v", err)
	}
	if len(headers) != 4 {
		t.Fatalf("expected 4 headers, got %d", len(headers))
	}
	if headers[0].GetName() != "X-Static" || headers[0].GetValue() != "value" {
		t.Fatalf("unexpected static header %#v", headers[0])
	}
	if headers[1].GetName() != "Authorization" || headers[1].GetScheme() != egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BEARER || headers[1].GetSecretId() != "secret-id" {
		t.Fatalf("unexpected bearer secret header %#v", headers[1])
	}
	if headers[2].GetName() != "Proxy-Authorization" || headers[2].GetScheme() != egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BASIC || headers[2].GetSecretId() != "basic-secret-id" || headers[2].GetUsername() != "x-access-token" {
		t.Fatalf("unexpected basic secret header %#v", headers[2])
	}
	if headers[3].GetScheme() != egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BASIC || headers[3].GetUsername() != "user" || headers[3].GetValue() != "pass" {
		t.Fatalf("unexpected basic literal header %#v", headers[3])
	}
}

func TestParseEgressHeadersRejectsInvalidInput(t *testing.T) {
	if _, err := parseEgressHeaders([]string{"=value"}); err == nil {
		t.Fatalf("expected missing name error")
	}
	if _, err := parseEgressHeaders([]string{"Authorization=basic-secret:only-an-id"}); err == nil {
		t.Fatalf("expected basic-secret without username to fail")
	}
	if _, err := parseEgressHeaders([]string{"Authorization=basic:only-a-user"}); err == nil {
		t.Fatalf("expected basic without password to fail")
	}
	if _, err := parseEgressHeaders([]string{"X-Test"}); err == nil {
		t.Fatalf("expected missing separator error")
	}
}

func TestEgressRuleOutputFrom(t *testing.T) {
	rule := &egressv1.EgressRule{
		Meta:           &egressv1.EntityMeta{Id: "rule-id", CreatedAt: timestamppb.Now(), UpdatedAt: timestamppb.Now()},
		OrganizationId: "org-id",
		Name:           "rule-name",
		Description:    "description",
		Matcher:        &egressv1.EgressRuleMatcher{DomainPattern: "api.example.com", Ports: []int32{443}, Methods: []string{"GET"}, PathPattern: "/v1/*"},
		Effect: &egressv1.EgressRuleEffect{
			Action: egressv1.EgressRuleAction_EGRESS_RULE_ACTION_ALLOW.Enum(),
			Inject: []*egressv1.EgressRuleHeader{{Name: "Authorization", Scheme: egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BEARER, Credential: &egressv1.EgressRuleHeader_SecretId{SecretId: "secret-id"}}},
		},
	}
	out, err := egressRuleOutputFrom(rule)
	if err != nil {
		t.Fatalf("build output: %v", err)
	}
	if out.ID != "rule-id" || out.OrganizationID != "org-id" || out.Name != "rule-name" {
		t.Fatalf("unexpected rule output %#v", out)
	}
	if out.Matcher.DomainPattern != "api.example.com" || len(out.Matcher.Ports) != 1 || out.Matcher.Ports[0] != 443 {
		t.Fatalf("unexpected matcher output %#v", out.Matcher)
	}
	if out.Effect.Action != "allow" || len(out.Effect.Headers) != 1 || out.Effect.Headers[0].SecretID != "secret-id" || out.Effect.Headers[0].Scheme != "bearer" {
		t.Fatalf("unexpected effect output %#v", out.Effect)
	}
}

func TestEgressRuleUpdateMergesPartialMatcher(t *testing.T) {
	service := &recordingEgressRulesGateway{current: &egressv1.EgressRule{
		Meta:           &egressv1.EntityMeta{Id: "rule-id", CreatedAt: timestamppb.Now(), UpdatedAt: timestamppb.Now()},
		OrganizationId: "org-id",
		Name:           "rule-name",
		Matcher:        &egressv1.EgressRuleMatcher{DomainPattern: "api.example.com", Ports: []int32{443}, Methods: []string{"GET"}, PathPattern: "/v1/*"},
		Effect: &egressv1.EgressRuleEffect{
			Action: egressv1.EgressRuleAction_EGRESS_RULE_ACTION_ALLOW.Enum(),
			Inject: []*egressv1.EgressRuleHeader{{Name: "Authorization", Scheme: egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BEARER, Credential: &egressv1.EgressRuleHeader_SecretId{SecretId: "secret-id"}}},
		},
	}}
	server := newEgressRulesGatewayTestServer(t, service)
	defer server.Close()

	cmd := newEgressRuleUpdateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetContext(withRunContext(context.Background(), &RunContext{Clients: newTestClients(t, server.URL), OutputFormat: output.FormatTable}))
	cmd.SetArgs([]string{"rule-id", "--port", "8443"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute update: %v", err)
	}
	matcher := service.updateRequest.GetMatcher()
	if matcher.GetDomainPattern() != "api.example.com" || matcher.GetPathPattern() != "/v1/*" {
		t.Fatalf("matcher did not preserve existing fields: %#v", matcher)
	}
	if len(matcher.GetPorts()) != 1 || matcher.GetPorts()[0] != 8443 {
		t.Fatalf("matcher ports = %#v", matcher.GetPorts())
	}
	if len(matcher.GetMethods()) != 1 || matcher.GetMethods()[0] != "GET" {
		t.Fatalf("matcher methods = %#v", matcher.GetMethods())
	}
	if len(service.updateRequest.GetEffect().GetInject()) != 0 {
		t.Fatalf("effect should not be sent for matcher-only update")
	}
}

func TestEgressCommandRegistration(t *testing.T) {
	cmd := newEgressCmd()
	if cmd.Use != "egress" {
		t.Fatalf("unexpected command use %q", cmd.Use)
	}
	ruleCmd, _, err := cmd.Find([]string{"rule"})
	if err != nil {
		t.Fatalf("find rule command: %v", err)
	}
	if ruleCmd == nil || ruleCmd.Use != "rule" {
		t.Fatalf("missing rule command")
	}
	for _, name := range []string{"create", "list", "get", "update", "delete", "attach", "detach"} {
		found, _, err := ruleCmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s command: %v", name, err)
		}
		if found == nil || found.Name() != name {
			t.Fatalf("missing %s command", name)
		}
	}
}
