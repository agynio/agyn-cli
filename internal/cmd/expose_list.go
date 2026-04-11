package cmd

import (
	"fmt"
	"os"

	"connectrpc.com/connect"
	exposev1 "github.com/agynio/agyn-cli/gen/agynio/api/expose/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

var exposeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active exposures",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		runContext, err := RunContextFrom(cmd)
		if err != nil {
			return err
		}

		client := gatewayv1connect.NewExposeGatewayClient(
			runContext.Clients.HTTPClient,
			runContext.Clients.BaseURL,
			runContext.Clients.ConnectOpts()...,
		)

		response, err := client.ListExposures(cmd.Context(), connect.NewRequest(&exposev1.ListExposuresRequest{}))
		if err != nil {
			return err
		}

		exposures := response.Msg.GetExposures()
		if runContext.OutputFormat == output.FormatTable {
			if len(exposures) == 0 {
				_, err := fmt.Fprint(os.Stdout, "No active exposures.\n")
				return err
			}

			table := output.Table{Headers: []string{"ID", "PORT", "URL", "STATUS"}}
			for _, exposure := range exposures {
				if exposure == nil {
					return fmt.Errorf("exposure missing in response")
				}

				meta := exposure.GetMeta()
				if meta == nil {
					return fmt.Errorf("exposure metadata missing in response")
				}

				table.Rows = append(table.Rows, []string{
					meta.GetId(),
					fmt.Sprintf("%d", exposure.GetPort()),
					exposure.GetUrl(),
					formatExposureStatus(exposure.GetStatus()),
				})
			}

			return output.Print(runContext.OutputFormat, table)
		}

		outputs := make([]exposureOutput, 0, len(exposures))
		for _, exposure := range exposures {
			if exposure == nil {
				return fmt.Errorf("exposure missing in response")
			}

			meta := exposure.GetMeta()
			if meta == nil {
				return fmt.Errorf("exposure metadata missing in response")
			}

			outputs = append(outputs, exposureOutput{
				ID:     meta.GetId(),
				Port:   exposure.GetPort(),
				URL:    exposure.GetUrl(),
				Status: formatExposureStatus(exposure.GetStatus()),
			})
		}

		return output.Print(runContext.OutputFormat, outputs)
	},
}
