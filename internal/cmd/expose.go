package cmd

import (
	"fmt"
	"strconv"

	exposev1 "github.com/agynio/agyn-cli/gen/agynio/api/expose/v1"
	"github.com/spf13/cobra"
)

type exposureOutput struct {
	ID     string `json:"id" yaml:"id"`
	Port   int32  `json:"port" yaml:"port"`
	URL    string `json:"url" yaml:"url"`
	Status string `json:"status" yaml:"status"`
}

var exposeCmd = &cobra.Command{
	Use:   "expose",
	Short: "Manage port exposures",
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
		return "unknown"
	}
}

func init() {
	rootCmd.AddCommand(exposeCmd)
	exposeCmd.AddCommand(exposeAddCmd)
	exposeCmd.AddCommand(exposeRemoveCmd)
	exposeCmd.AddCommand(exposeListCmd)
}
