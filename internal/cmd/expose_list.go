package cmd

import (
	"fmt"

	"connectrpc.com/connect"
	gatewayv1 "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

func newExposeListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active exposures",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			client := gatewayv1connect.NewExposeGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)

			response, err := client.ListExposures(cmd.Context(), connect.NewRequest(&gatewayv1.ListExposuresRequest{}))
			if err != nil {
				return err
			}

			exposures := response.Msg.GetExposures()
			outputs := make([]exposureOutput, 0, len(exposures))
			rows := make([][]string, 0, len(exposures))
			for _, exposure := range exposures {
				outputData, err := exposureOutputFrom(exposure)
				if err != nil {
					return err
				}
				outputs = append(outputs, outputData)
				rows = append(rows, []string{
					outputData.ID,
					fmt.Sprintf("%d", outputData.Port),
					outputData.URL,
					outputData.Status,
				})
			}

			if runContext.OutputFormat == output.FormatTable {
				if len(outputs) == 0 {
					_, err := fmt.Fprint(cmd.OutOrStdout(), "No active exposures.\n")
					return err
				}

				table := output.Table{
					Headers: []string{"ID", "PORT", "URL", "STATUS"},
					Rows:    rows,
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, outputs)
		},
	}

	return cmd
}
