package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	filesv1 "github.com/agynio/agyn-cli/gen/agynio/api/files/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

const uploadChunkSize = 64 * 1024

type filesUploadArgs struct {
	filename    string
	contentType string
}

func newFilesUploadCmd() *cobra.Command {
	args := &filesUploadArgs{}
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			return runFilesUpload(cmd, args, input[0])
		},
	}
	cmd.Flags().StringVar(&args.filename, "filename", "", "Override filename")
	cmd.Flags().StringVar(&args.contentType, "type", "", "Override content type")
	return cmd
}

func runFilesUpload(cmd *cobra.Command, args *filesUploadArgs, path string) (err error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	if runContext.Clients == nil {
		return fmt.Errorf("gateway client unavailable")
	}

	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("path is required")
	}

	file, err := os.Open(trimmedPath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close file: %w", closeErr)
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", trimmedPath)
	}

	filename := strings.TrimSpace(args.filename)
	if filename == "" {
		filename = filepath.Base(trimmedPath)
	}
	if strings.TrimSpace(filename) == "" {
		return fmt.Errorf("filename is required")
	}

	contentType := strings.TrimSpace(args.contentType)
	if contentType == "" {
		contentType = inferContentType(trimmedPath)
	}

	client := gatewayv1connect.NewFilesGatewayClient(
		runContext.Clients.HTTPClient,
		runContext.Clients.BaseURL,
		runContext.Clients.ConnectOpts()...,
	)
	stream := client.UploadFile(cmd.Context())
	if err := stream.Send(&filesv1.UploadFileRequest{
		Payload: &filesv1.UploadFileRequest_Metadata{
			Metadata: &filesv1.UploadFileMetadata{
				Filename:    filename,
				ContentType: contentType,
				SizeBytes:   info.Size(),
			},
		},
	}); err != nil {
		return err
	}

	if err := sendFileChunks(stream, file); err != nil {
		return err
	}

	response, err := stream.CloseAndReceive()
	if err != nil {
		return err
	}
	outputData, err := fileOutputFrom(response.Msg.GetFile())
	if err != nil {
		return err
	}

	if runContext.OutputFormat == output.FormatTable {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), outputData.ID)
		return err
	}

	return output.Print(runContext.OutputFormat, outputData)
}

func sendFileChunks(stream *connect.ClientStreamForClient[filesv1.UploadFileRequest, filesv1.UploadFileResponse], reader io.Reader) error {
	buffer := make([]byte, uploadChunkSize)
	for {
		readBytes, err := reader.Read(buffer)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read file: %w", err)
		}
		if readBytes > 0 {
			chunk := make([]byte, readBytes)
			copy(chunk, buffer[:readBytes])
			if err := stream.Send(&filesv1.UploadFileRequest{
				Payload: &filesv1.UploadFileRequest_Chunk{
					Chunk: &filesv1.UploadFileChunk{Data: chunk},
				},
			}); err != nil {
				return fmt.Errorf("send file chunk: %w", err)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
