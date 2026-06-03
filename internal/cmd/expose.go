package cmd

import (
	"fmt"
	"strconv"

	exposev1 "github.com/agynio/agyn-cli/gen/agynio/api/expose/v1"
	"github.com/spf13/cobra"
)

type exposureOutput struct {
	ID                   string `json:"id" yaml:"id"`
	Port                 int32  `json:"port" yaml:"port"`
	URL                  string `json:"url" yaml:"url"`
	Status               string `json:"status" yaml:"status"`
	OpenZitiServiceID    string `json:"openziti_service_id" yaml:"openziti_service_id"`
	OpenZitiBindPolicyID string `json:"openziti_bind_policy_id" yaml:"openziti_bind_policy_id"`
	OpenZitiDialPolicyID string `json:"openziti_dial_policy_id" yaml:"openziti_dial_policy_id"`
}

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
		ID:                   meta.GetId(),
		Port:                 exposure.GetPort(),
		URL:                  exposure.GetUrl(),
		Status:               formatExposureStatus(exposure.GetStatus()),
		OpenZitiServiceID:    exposure.GetOpenzitiServiceId(),
		OpenZitiBindPolicyID: exposure.GetOpenzitiBindPolicyId(),
		OpenZitiDialPolicyID: exposure.GetOpenzitiDialPolicyId(),
	}, nil
}

func init() {
	rootCmd.AddCommand(newExposeCmd())
}
