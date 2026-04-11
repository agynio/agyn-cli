package cmd

import (
	"fmt"

	"connectrpc.com/connect"
	exposev1 "github.com/agynio/agyn-cli/gen/agynio/api/expose/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

var exposeAddCmd = &cobra.Command{
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

		client := gatewayv1connect.NewExposeGatewayClient(
			runContext.Clients.HTTPClient,
			runContext.Clients.BaseURL,
			runContext.Clients.ConnectOpts()...,
		)

		response, err := client.AddExposure(cmd.Context(), connect.NewRequest(&exposev1.AddExposureRequest{
			Port: int32(port),
		}))
		if err != nil {
			return err
		}

		exposure := response.Msg.GetExposure()
		if exposure == nil {
			return fmt.Errorf("exposure missing in response")
		}

		meta := exposure.GetMeta()
		if meta == nil {
			return fmt.Errorf("exposure metadata missing in response")
		}

		status := formatExposureStatus(exposure.GetStatus())
		if runContext.OutputFormat == output.FormatTable {
			table := output.Table{
				Headers: []string{"ID", "PORT", "URL", "STATUS"},
				Rows: [][]string{{
					meta.GetId(),
					fmt.Sprintf("%d", exposure.GetPort()),
					exposure.GetUrl(),
					status,
				}},
			}
			return output.Print(runContext.OutputFormat, table)
		}

		payload := exposureOutput{
			ID:     meta.GetId(),
			Port:   exposure.GetPort(),
			URL:    exposure.GetUrl(),
			Status: status,
		}
		return output.Print(runContext.OutputFormat, payload)
	},
}
