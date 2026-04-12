package cmd

import (
	"fmt"
	"testing"

	exposev1 "github.com/agynio/agyn-cli/gen/agynio/api/expose/v1"
)

func TestParsePort(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    uint32
		wantErr bool
	}{
		{name: "minimum", input: "1", want: 1},
		{name: "maximum", input: "65535", want: 65535},
		{name: "common", input: "8080", want: 8080},
		{name: "leading zeros", input: "001", want: 1},
		{name: "zero", input: "0", wantErr: true},
		{name: "too large", input: "65536", wantErr: true},
		{name: "negative", input: "-1", wantErr: true},
		{name: "alpha", input: "abc", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePort(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", test.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %q: %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("expected %d, got %d", test.want, got)
			}
		})
	}
}

func TestFormatExposureStatus(t *testing.T) {
	tests := map[exposev1.ExposureStatus]string{
		exposev1.ExposureStatus_EXPOSURE_STATUS_UNSPECIFIED:  "unspecified",
		exposev1.ExposureStatus_EXPOSURE_STATUS_PROVISIONING: "provisioning",
		exposev1.ExposureStatus_EXPOSURE_STATUS_ACTIVE:       "active",
		exposev1.ExposureStatus_EXPOSURE_STATUS_FAILED:       "failed",
		exposev1.ExposureStatus_EXPOSURE_STATUS_REMOVING:     "removing",
		exposev1.ExposureStatus(99):                          "ExposureStatus(99)",
	}

	for status, want := range tests {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			got := formatExposureStatus(status)
			if got != want {
				t.Fatalf("expected %q, got %q", want, got)
			}
		})
	}
}

func TestResolveWorkloadIDPrefersEnv(t *testing.T) {
	t.Setenv(workloadIDEnv, "workload-123")
	t.Setenv("HOSTNAME", "workload-456")

	got, err := resolveWorkloadID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "workload-123" {
		t.Fatalf("expected WORKLOAD_ID, got %q", got)
	}
}

func TestResolveWorkloadIDFromHostname(t *testing.T) {
	t.Setenv(workloadIDEnv, "")
	t.Setenv("HOSTNAME", "workload-456")

	got, err := resolveWorkloadID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "456" {
		t.Fatalf("expected hostname suffix, got %q", got)
	}
}

func TestResolveWorkloadIDMissing(t *testing.T) {
	t.Setenv(workloadIDEnv, "")
	t.Setenv("HOSTNAME", "agent-456")

	_, err := resolveWorkloadID()
	if err == nil {
		t.Fatal("expected error for missing workload id")
	}
}

func TestResolveWorkloadIDEmptyHostnameSuffix(t *testing.T) {
	t.Setenv(workloadIDEnv, "")
	t.Setenv("HOSTNAME", "workload-")

	_, err := resolveWorkloadID()
	if err == nil {
		t.Fatal("expected error for empty workload suffix")
	}
}
