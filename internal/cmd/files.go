package cmd

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	filesv1 "github.com/agynio/agyn-cli/gen/agynio/api/files/v1"
	"github.com/spf13/cobra"
)

const (
	defaultContentType     = "application/octet-stream"
	defaultExpiryFlagValue = "1h"
)

type fileOutput struct {
	ID          string `json:"id" yaml:"id"`
	Filename    string `json:"filename" yaml:"filename"`
	ContentType string `json:"content_type" yaml:"content_type"`
	SizeBytes   int64  `json:"size_bytes" yaml:"size_bytes"`
	CreatedAt   string `json:"created_at" yaml:"created_at"`
}

type downloadOutput struct {
	Path      string `json:"path" yaml:"path"`
	FileID    string `json:"file_id" yaml:"file_id"`
	SizeBytes int64  `json:"size_bytes" yaml:"size_bytes"`
}

type downloadURLOutput struct {
	URL       string `json:"url" yaml:"url"`
	ExpiresAt string `json:"expires_at" yaml:"expires_at"`
}

func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Manage files",
	}

	cmd.AddCommand(newFilesUploadCmd())
	cmd.AddCommand(newFilesDownloadCmd())
	cmd.AddCommand(newFilesInfoCmd())
	cmd.AddCommand(newFilesURLCmd())

	return cmd
}

func fileOutputFrom(fileInfo *filesv1.FileInfo) (fileOutput, error) {
	if fileInfo == nil {
		return fileOutput{}, fmt.Errorf("file info missing from response")
	}
	return fileOutput{
		ID:          fileInfo.GetId(),
		Filename:    fileInfo.GetFilename(),
		ContentType: fileInfo.GetContentType(),
		SizeBytes:   fileInfo.GetSizeBytes(),
		CreatedAt:   formatTimestamp(fileInfo.GetCreatedAt()),
	}, nil
}

func inferContentType(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == "" {
		return defaultContentType
	}
	contentType := mime.TypeByExtension(extension)
	if contentType == "" {
		return defaultContentType
	}
	return contentType
}

func resolveDownloadPath(outputPath string, fileInfo *filesv1.FileInfo) (string, error) {
	trimmed := strings.TrimSpace(outputPath)
	if trimmed != "" {
		return trimmed, nil
	}
	if fileInfo == nil {
		return "", fmt.Errorf("file metadata missing in response")
	}
	filename := strings.TrimSpace(fileInfo.GetFilename())
	if filename == "" {
		return "", fmt.Errorf("file filename missing in response")
	}
	return filepath.Join(".", filepath.Base(filename)), nil
}

func parseExpiryDuration(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("expiry is required")
	}
	duration, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse expiry: %w", err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("expiry must be positive")
	}
	return duration, nil
}

func init() {
	rootCmd.AddCommand(newFilesCmd())
}
