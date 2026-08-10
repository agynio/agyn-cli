package cmd

import (
	"testing"

	groupsv1 "github.com/agynio/agyn-cli/gen/agynio/api/groups/v1"
	networksv1 "github.com/agynio/agyn-cli/gen/agynio/api/networks/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPrivateNetworkCommandRegistration(t *testing.T) {
	commands := map[string][]string{
		"network":  {"create", "list", "get", "update", "delete"},
		"tunnel":   {"create", "list", "get", "delete"},
		"resource": {"create", "list", "get", "update", "delete", "grant"},
		"group":    {"create", "list", "get", "update", "delete", "member"},
	}
	for use, children := range commands {
		cmd := commandForUse(use)
		if cmd.Use != use {
			t.Fatalf("unexpected command use %q", cmd.Use)
		}
		for _, child := range children {
			found, _, err := cmd.Find([]string{child})
			if err != nil {
				t.Fatalf("find %s %s command: %v", use, child, err)
			}
			if found == nil || found.Name() != child {
				t.Fatalf("missing %s %s command", use, child)
			}
		}
	}
}

func TestPrivateNetworkParsers(t *testing.T) {
	protocol, err := parsePrivateResourceProtocol(" https ")
	if err != nil {
		t.Fatalf("parse protocol: %v", err)
	}
	if protocol != networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_HTTPS {
		t.Fatalf("unexpected protocol %s", protocol)
	}
	if _, err := parsePrivateResourceProtocol("udp"); err == nil {
		t.Fatalf("expected invalid protocol error")
	}

	principalType, err := parsePrincipalType("group")
	if err != nil {
		t.Fatalf("parse principal type: %v", err)
	}
	if principalType != networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_GROUP {
		t.Fatalf("unexpected principal type %s", principalType)
	}
	if _, err := parsePrincipalType("runner"); err == nil {
		t.Fatalf("expected invalid principal type error")
	}

	memberType, err := parseGroupMemberType("app")
	if err != nil {
		t.Fatalf("parse member type: %v", err)
	}
	if memberType != groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_APP {
		t.Fatalf("unexpected member type %s", memberType)
	}
	if _, err := parseGroupMemberType("runner"); err == nil {
		t.Fatalf("expected invalid member type error")
	}
}

func TestPrivateNetworkOutputs(t *testing.T) {
	now := timestamppb.Now()
	network, err := networkOutputFrom(&networksv1.Network{
		Meta:              &networksv1.EntityMeta{Id: "network-id", CreatedAt: now, UpdatedAt: now},
		OrganizationId:    "org-id",
		Name:              "corp",
		ProvisioningState: networksv1.ProvisioningState_PROVISIONING_STATE_ACTIVE,
	})
	if err != nil {
		t.Fatalf("network output: %v", err)
	}
	if network.ID != "network-id" || network.ProvisioningState != "active" {
		t.Fatalf("unexpected network output %#v", network)
	}

	resource, err := privateResourceOutputFrom(&networksv1.PrivateResource{
		Meta:              &networksv1.EntityMeta{Id: "resource-id", CreatedAt: now, UpdatedAt: now},
		OrganizationId:    "org-id",
		NetworkId:         "network-id",
		Name:              "postgres",
		Protocol:          networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_TCP,
		TargetHost:        "db.internal",
		TargetPorts:       []int32{5432},
		InterceptHost:     "db.corp.private",
		InterceptPorts:    []int32{5432},
		ProvisioningState: networksv1.ProvisioningState_PROVISIONING_STATE_ACTIVE,
	})
	if err != nil {
		t.Fatalf("resource output: %v", err)
	}
	if resource.Protocol != "tcp" || resource.TargetPorts[0] != 5432 || resource.InterceptHost != "db.corp.private" {
		t.Fatalf("unexpected resource output %#v", resource)
	}

	group, err := groupOutputFrom(&groupsv1.Group{
		Meta:           &groupsv1.EntityMeta{Id: "group-id", CreatedAt: now, UpdatedAt: now},
		OrganizationId: "org-id",
		Name:           "engineering",
		Source:         groupsv1.GroupSource_GROUP_SOURCE_PLATFORM,
	})
	if err != nil {
		t.Fatalf("group output: %v", err)
	}
	if group.ID != "group-id" || group.Source != "platform" {
		t.Fatalf("unexpected group output %#v", group)
	}
}

func commandForUse(use string) *cobra.Command {
	switch use {
	case "network":
		return newNetworkCmd()
	case "tunnel":
		return newTunnelCmd()
	case "resource":
		return newResourceCmd()
	case "group":
		return newGroupCmd()
	default:
		panic("unsupported command " + use)
	}
}

// An environment is the only principal that reaches a sandbox, so the CLI has
// to be able to name one.
func TestParsePrincipalTypeAcceptsEnvironment(t *testing.T) {
	principalType, err := parsePrincipalType("environment")
	if err != nil {
		t.Fatalf("parsePrincipalType: %v", err)
	}
	if principalType != networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_ENVIRONMENT {
		t.Fatalf("got %v", principalType)
	}
	if got := principalTypeString(principalType); got != "environment" {
		t.Fatalf("principalTypeString: got %q", got)
	}
}

// A grant made by a newer server is still a real grant. Listing one must not
// take the process down, which is what the previous panic did.
func TestPrincipalTypeStringSurvivesAnUnknownPrincipal(t *testing.T) {
	unknown := networksv1.PrivateResourceAccessPrincipalType(999)
	got := principalTypeString(unknown)
	if got == "" {
		t.Fatal("expected a name for an unknown principal type")
	}
}
