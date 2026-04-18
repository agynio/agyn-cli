package cmd

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"
	filesv1 "github.com/agynio/agyn-cli/gen/agynio/api/files/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"
)

type filesURLArgs struct {
	expiry string
}

func newFilesURLCmd() *cobra.Command {
	args := &filesURLArgs{}
	cmd := &cobra.Command{
		Use:   "url <file-id>",
		Short: "Get a file download URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			return runFilesURL(cmd, args, input[0])
		},
	}
	cmd.Flags().StringVar(&args.expiry, "expiry", defaultExpiryFlagValue, "URL expiry duration (e.g. 1h, 30m)")
	return cmd
}

func runFilesURL(cmd *cobra.Command, args *filesURLArgs, fileID string) error {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	if runContext.Clients == nil {
		return fmt.Errorf("gateway client unavailable")
	}

	trimmedID := strings.TrimSpace(fileID)
	if trimmedID == "" {
		return fmt.Errorf("file-id is required")
	}

	expiry, err := parseExpiryDuration(args.expiry)
	if err != nil {
		return err
	}

	client := gatewayv1connect.NewFilesGatewayClient(
		runContext.Clients.HTTPClient,
		runContext.Clients.BaseURL,
		runContext.Clients.ConnectOpts()...,
	)
	response, err := client.GetDownloadUrl(cmd.Context(), connect.NewRequest(&filesv1.GetDownloadUrlRequest{
		FileId: trimmedID,
		Expiry: durationpb.New(expiry),
	}))
	if err != nil {
		return err
	}

	outputData := downloadURLOutput{
		URL:       response.Msg.GetUrl(),
		ExpiresAt: formatTimestamp(response.Msg.GetExpiresAt()),
	}

	if runContext.OutputFormat == output.FormatTable {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), outputData.URL)
		return err
	}

	return output.Print(runContext.OutputFormat, outputData)
}
