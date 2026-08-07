package cmd

import (
	"testing"

	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
)

// An environment holds a catalog record and a tag, never a registry address.
func TestSplitImageReference(t *testing.T) {
	id, tag, err := splitImageReference("11111111-1111-1111-1111-111111111111:3.12")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if id != "11111111-1111-1111-1111-111111111111" || tag != "3.12" {
		t.Fatalf("unexpected split: %q %q", id, tag)
	}
	for _, bad := range []string{"", "no-tag", ":leading", "trailing:"} {
		if _, _, err := splitImageReference(bad); err == nil {
			t.Fatalf("expected %q to be refused", bad)
		}
	}
}

// availability is required on create with no default, because running in an
// environment reaches its secrets.
func TestParseAvailability(t *testing.T) {
	internal, err := parseAvailability("internal")
	if err != nil || internal != agentsv1.EnvironmentAvailability_ENVIRONMENT_AVAILABILITY_INTERNAL {
		t.Fatalf("internal: %v %v", internal, err)
	}
	private, err := parseAvailability("PRIVATE")
	if err != nil || private != agentsv1.EnvironmentAvailability_ENVIRONMENT_AVAILABILITY_PRIVATE {
		t.Fatalf("private: %v %v", private, err)
	}
	if _, err := parseAvailability("public"); err == nil {
		t.Fatal("expected an unknown availability to be refused")
	}
}

func TestParseEnvironmentRole(t *testing.T) {
	for raw, want := range map[string]agentsv1.EnvironmentRole{
		"owner":      agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_OWNER,
		"maintainer": agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_MAINTAINER,
		"user":       agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_USER,
	} {
		got, err := parseEnvironmentRole(raw)
		if err != nil || got != want {
			t.Fatalf("%s: got %v err %v", raw, got, err)
		}
	}
	if _, err := parseEnvironmentRole("participant"); err == nil {
		t.Fatal("expected an agent role name to be refused on an environment")
	}
}

// --size is the whole persistence control: the resource makes size and
// persistence biconditional, so a separate flag could only contradict it.
func TestVolumesAddHasNoPersistentFlag(t *testing.T) {
	cmd := newEnvironmentVolumesCmd()
	for _, sub := range cmd.Commands() {
		if sub.Name() != "add" {
			continue
		}
		if sub.Flags().Lookup("persistent") != nil {
			t.Fatal("expected no --persistent flag; --size is what makes a volume persistent")
		}
		if sub.Flags().Lookup("size") == nil {
			t.Fatal("expected a --size flag")
		}
		return
	}
	t.Fatal("expected an add subcommand")
}
