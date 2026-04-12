package cmd

import (
	"fmt"

	"connectrpc.com/connect"
	gatewayv1 "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/spf13/cobra"
)

func newExposeRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
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
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			client := gatewayv1connect.NewExposeGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)

			_, err = client.RemoveExposureForCaller(cmd.Context(), connect.NewRequest(&gatewayv1.RemoveExposureForCallerRequest{
				Port: int32(port),
			}))
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Exposure on port %d removed.\n", port)
			return err
		},
	}

	return cmd
}
