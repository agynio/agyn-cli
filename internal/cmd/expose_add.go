package cmd

import (
	"fmt"

	"connectrpc.com/connect"
	gatewayv1 "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

func newExposeAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <port>",
		Short: "Expose a port",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := parsePort(args[0])
			if err != nil {
				return err
			}

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

			response, err := client.AddExposure(cmd.Context(), connect.NewRequest(&gatewayv1.AddExposureRequest{
				Port: int32(port),
			}))
			if err != nil {
				return err
			}

			outputData, err := exposureOutputFrom(response.Msg.GetExposure())
			if err != nil {
				return err
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "PORT", "URL", "STATUS"},
					Rows: [][]string{{
						outputData.ID,
						fmt.Sprintf("%d", outputData.Port),
						outputData.URL,
						outputData.Status,
					}},
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, outputData)
		},
	}

	return cmd
}
