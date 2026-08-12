package local

import "testing"

// kubectl omits readyReplicas entirely on a workload with none, so the line for
// the workload a wait most wants to report is the one with a field missing.
func TestParseWorkloadsHandlesAMissingReadyCount(t *testing.T) {
	workloads := parseWorkloads("gateway 3 3\nagents  1\nchat 0 2\nkeycloak 1 1\n")

	if len(workloads) != 4 {
		t.Fatalf("expected 4 workloads, got %d: %+v", len(workloads), workloads)
	}
	if workloads[1].Name != "agents" || workloads[1].Ready != 0 || workloads[1].Desired != 1 {
		t.Fatalf("expected agents to be 0 of 1, got %+v", workloads[1])
	}
	rolling := 0
	for _, workload := range workloads {
		if workload.Rolling() {
			rolling++
		}
	}
	if rolling != 2 {
		t.Fatalf("expected 2 rolling workloads, got %d", rolling)
	}
}

// A workload scaled to zero is not one a wait is waiting for.
func TestParseWorkloadsIgnoresScaledDownWorkloads(t *testing.T) {
	if got := parseWorkloads("idle 0 0\n"); len(got) != 0 {
		t.Fatalf("expected scaled-down workloads to be dropped, got %+v", got)
	}
}

// Readiness is two signals, and the second one is the whole reason the wait
// exists: the endpoints answer before the platform has provisioned itself.
func TestReadinessIsNotReadyUntilBothSignalsHold(t *testing.T) {
	pending := Readiness{Pending: []string{"the platform is provisioning its organization"}}
	if pending.Ready() {
		t.Fatal("expected a platform with no organization id to be not ready")
	}
	if pending.Missing() == "" {
		t.Fatal("expected a timeout to be able to name what is missing")
	}
	if !(Readiness{Organization: "8f3a"}).Ready() {
		t.Fatal("expected both signals holding to be ready")
	}
}
