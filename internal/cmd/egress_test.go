package cmd

import (
	"testing"

	egressv1 "github.com/agynio/agyn-cli/gen/agynio/api/egress/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
		"Proxy-Authorization=basic-secret:basic-secret-id",
	})
	if err != nil {
		t.Fatalf("parse headers: %v", err)
	}
	if len(headers) != 3 {
		t.Fatalf("expected 3 headers, got %d", len(headers))
	}
	if headers[0].GetName() != "X-Static" || headers[0].GetValue() != "value" {
		t.Fatalf("unexpected static header %#v", headers[0])
	}
	if headers[1].GetName() != "Authorization" || headers[1].GetScheme() != egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BEARER || headers[1].GetSecretId() != "secret-id" {
		t.Fatalf("unexpected bearer secret header %#v", headers[1])
	}
	if headers[2].GetName() != "Proxy-Authorization" || headers[2].GetScheme() != egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BASIC || headers[2].GetSecretId() != "basic-secret-id" {
		t.Fatalf("unexpected basic secret header %#v", headers[2])
	}
}

func TestParseEgressHeadersRejectsInvalidInput(t *testing.T) {
	if _, err := parseEgressHeaders([]string{"=value"}); err == nil {
		t.Fatalf("expected missing name error")
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
