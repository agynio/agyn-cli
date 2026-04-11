package cmd

import (
	"fmt"
	"os"

	"connectrpc.com/connect"
	exposev1 "github.com/agynio/agyn-cli/gen/agynio/api/expose/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/spf13/cobra"
)

var exposeRemoveCmd = &cobra.Command{
	Use:   "remove <port>",
	Short: "Remove an exposure",
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

		_, err = client.RemoveExposure(cmd.Context(), connect.NewRequest(&exposev1.RemoveExposureRequest{
			Port: int32(port),
		}))
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(os.Stdout, "Exposure on port %d removed.\n", port)
		return err
	},
}
