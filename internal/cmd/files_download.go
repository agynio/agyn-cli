package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"connectrpc.com/connect"
	filesv1 "github.com/agynio/agyn-cli/gen/agynio/api/files/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type filesDownloadArgs struct {
	outputPath string
}

func newFilesDownloadCmd() *cobra.Command {
	args := &filesDownloadArgs{}
	cmd := &cobra.Command{
		Use:   "download <file-id>",
		Short: "Download a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			return runFilesDownload(cmd, args, input[0])
		},
	}
	cmd.Flags().StringVar(&args.outputPath, "output-path", "", "Destination path")
	return cmd
}

func runFilesDownload(cmd *cobra.Command, args *filesDownloadArgs, fileID string) (err error) {
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
	metadataResponse, err := client.GetFileMetadata(cmd.Context(), connect.NewRequest(&filesv1.GetFileMetadataRequest{
		FileId: trimmedID,
	}))
	if err != nil {
		return err
	}
	fileInfo := metadataResponse.Msg.GetFile()
	if fileInfo == nil {
		return fmt.Errorf("file metadata missing from response")
	}

	outputPath, err := resolveDownloadPath(args.outputPath, fileInfo)
	if err != nil {
		return err
	}

	outputFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		closeErr := outputFile.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close output file: %w", closeErr)
		}
		if err != nil && cleanup {
			if removeErr := os.Remove(outputPath); removeErr != nil && !os.IsNotExist(removeErr) {
				err = fmt.Errorf("%v (cleanup failed: %w)", err, removeErr)
			}
		}
	}()

	stream, err := client.GetFileContent(cmd.Context(), connect.NewRequest(&filesv1.GetFileContentRequest{
		FileId: trimmedID,
	}))
	if err != nil {
		return err
	}
	if err := writeFileContent(stream, outputFile); err != nil {
		return err
	}

	cleanup = false

	outputData := downloadOutput{
		Path:      outputPath,
		FileID:    trimmedID,
		SizeBytes: fileInfo.GetSizeBytes(),
	}
	if runContext.OutputFormat == output.FormatTable {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), outputPath)
		return err
	}

	return output.Print(runContext.OutputFormat, outputData)
}

func writeFileContent(stream *connect.ServerStreamForClient[filesv1.GetFileContentResponse], outputFile *os.File) error {
	for stream.Receive() {
		chunk := stream.Msg()
		if chunk == nil {
			return fmt.Errorf("file content chunk missing from response")
		}
		data := chunk.GetChunkData()
		if len(data) == 0 {
			continue
		}
		written, err := outputFile.Write(data)
		if err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		if written != len(data) {
			return io.ErrShortWrite
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return nil
}
