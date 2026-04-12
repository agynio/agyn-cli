package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	exposev1 "github.com/agynio/agyn-cli/gen/agynio/api/expose/v1"
	"github.com/spf13/cobra"
)

type exposureOutput struct {
	ID     string `json:"id" yaml:"id"`
	Port   int32  `json:"port" yaml:"port"`
	URL    string `json:"url" yaml:"url"`
	Status string `json:"status" yaml:"status"`
}

const (
	workloadIDEnv          = "WORKLOAD_ID"
	agentIDEnv             = "AGENT_ID"
	workloadHostnamePrefix = "workload-"
)

func newExposeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "expose",
		Short: "Manage port exposures",
	}

	cmd.AddCommand(newExposeAddCmd())
	cmd.AddCommand(newExposeRemoveCmd())
	cmd.AddCommand(newExposeListCmd())

	return cmd
}

func parsePort(value string) (uint32, error) {
	port, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", value, err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}
	return uint32(port), nil
}

func resolveWorkloadID() (string, error) {
	if value := strings.TrimSpace(os.Getenv(workloadIDEnv)); value != "" {
		return value, nil
	}

	hostname := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if strings.HasPrefix(hostname, workloadHostnamePrefix) {
		suffix := strings.TrimPrefix(hostname, workloadHostnamePrefix)
		if suffix != "" {
			return suffix, nil
		}
	}

	return "", fmt.Errorf("workload id unavailable; set %s or ensure HOSTNAME starts with %q", workloadIDEnv, workloadHostnamePrefix)
}

func agentIDFromEnv() string {
	return strings.TrimSpace(os.Getenv(agentIDEnv))
}

func formatExposureStatus(status exposev1.ExposureStatus) string {
	switch status {
	case exposev1.ExposureStatus_EXPOSURE_STATUS_UNSPECIFIED:
		return "unspecified"
	case exposev1.ExposureStatus_EXPOSURE_STATUS_PROVISIONING:
		return "provisioning"
	case exposev1.ExposureStatus_EXPOSURE_STATUS_ACTIVE:
		return "active"
	case exposev1.ExposureStatus_EXPOSURE_STATUS_FAILED:
		return "failed"
	case exposev1.ExposureStatus_EXPOSURE_STATUS_REMOVING:
		return "removing"
	default:
		return fmt.Sprintf("ExposureStatus(%d)", status)
	}
}

func exposureOutputFrom(exposure *exposev1.Exposure) (exposureOutput, error) {
	if exposure == nil {
		return exposureOutput{}, fmt.Errorf("exposure missing in response")
	}
	meta := exposure.GetMeta()
	if meta == nil {
		return exposureOutput{}, fmt.Errorf("exposure metadata missing in response")
	}
	return exposureOutput{
		ID:     meta.GetId(),
		Port:   exposure.GetPort(),
		URL:    exposure.GetUrl(),
		Status: formatExposureStatus(exposure.GetStatus()),
	}, nil
}
