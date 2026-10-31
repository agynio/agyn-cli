package cmd

import (
	"testing"

	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
)

// Every status the API defines has a name at the shell. A value the CLI has not
// learned is listed as "unknown", which for a hibernated sandbox — asleep, its
// shells intact, one `connect` from back — reads as something gone wrong.
func TestEverySandboxStatusHasALabel(t *testing.T) {
	labels := map[agentsv1.SandboxStatus]string{
		agentsv1.SandboxStatus_SANDBOX_STATUS_UNSPECIFIED: "unknown",
		agentsv1.SandboxStatus_SANDBOX_STATUS_STARTING:    "starting",
		agentsv1.SandboxStatus_SANDBOX_STATUS_RUNNING:     "running",
		agentsv1.SandboxStatus_SANDBOX_STATUS_STOPPED:     "stopped",
		agentsv1.SandboxStatus_SANDBOX_STATUS_FAILED:      "failed",
		agentsv1.SandboxStatus_SANDBOX_STATUS_TERMINATED:  "terminated",
		agentsv1.SandboxStatus_SANDBOX_STATUS_HIBERNATED:  "hibernated",
	}
	for number, name := range agentsv1.SandboxStatus_name {
		status := agentsv1.SandboxStatus(number)
		want, ok := labels[status]
		if !ok {
			t.Errorf("%s has no label here; name it in sandboxStatusLabel and in this table", name)
			continue
		}
		if got := sandboxStatusLabel(status); got != want {
			t.Errorf("sandboxStatusLabel(%s) = %q, want %q", name, got, want)
		}
	}
}
