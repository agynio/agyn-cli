package cmd

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"
	filesv1 "github.com/agynio/agyn-cli/gen/agynio/api/files/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

func newFilesInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info <file-id>",
		Short: "Get file metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			return runFilesInfo(cmd, input[0])
		},
	}
	return cmd
}

func runFilesInfo(cmd *cobra.Command, fileID string) error {
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

	client := gatewayv1connect.NewFilesGatewayClient(
		runContext.Clients.HTTPClient,
		runContext.Clients.BaseURL,
		runContext.Clients.ConnectOpts()...,
	)
	response, err := client.GetFileMetadata(cmd.Context(), connect.NewRequest(&filesv1.GetFileMetadataRequest{
		FileId: trimmedID,
	}))
	if err != nil {
		return err
	}
	outputData, err := fileOutputFrom(response.Msg.GetFile())
	if err != nil {
		return err
	}

	if runContext.OutputFormat == output.FormatTable {
		table := output.Table{
			Headers: []string{"KEY", "VALUE"},
			Rows: [][]string{
				{"ID", outputData.ID},
				{"FILENAME", outputData.Filename},
				{"CONTENT_TYPE", outputData.ContentType},
				{"SIZE_BYTES", fmt.Sprintf("%d", outputData.SizeBytes)},
				{"CREATED_AT", outputData.CreatedAt},
			},
		}
		return output.Print(runContext.OutputFormat, table)
	}

	return output.Print(runContext.OutputFormat, outputData)
}
