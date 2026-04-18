package cmd

import (
	"mime"
	"path/filepath"
	"testing"
	"time"

	filesv1 "github.com/agynio/agyn-cli/gen/agynio/api/files/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestInferContentType(t *testing.T) {
	textType := mime.TypeByExtension(".txt")
	if textType == "" {
		t.Fatal("text/plain missing from mime registry")
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "text file", path: "note.txt", want: textType},
		{name: "uppercase extension", path: "note.TXT", want: textType},
		{name: "unknown extension", path: "archive.unknown", want: defaultContentType},
		{name: "no extension", path: "LICENSE", want: defaultContentType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := inferContentType(test.path)
			if got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}

func TestFileOutputFrom(t *testing.T) {
	createdAt := timestamppb.New(time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC))
	info := &filesv1.FileInfo{
		Id:          "file-1",
		Filename:    "report.txt",
		ContentType: "text/plain",
		SizeBytes:   123,
		CreatedAt:   createdAt,
	}
	outputData, err := fileOutputFrom(info)
	if err != nil {
		t.Fatalf("fileOutputFrom: %v", err)
	}
	if outputData.ID != "file-1" || outputData.Filename != "report.txt" {
		t.Fatalf("unexpected output: %#v", outputData)
	}
	if outputData.CreatedAt != formatTimestamp(createdAt) {
		t.Fatalf("unexpected created_at: %s", outputData.CreatedAt)
	}

	if _, err := fileOutputFrom(nil); err == nil {
		t.Fatal("expected error for nil file info")
	}
}

func TestResolveDownloadPath(t *testing.T) {
	info := &filesv1.FileInfo{Filename: "report.txt"}
	outputPath, err := resolveDownloadPath("", info)
	if err != nil {
		t.Fatalf("resolveDownloadPath: %v", err)
	}
	expected := filepath.Join(".", "report.txt")
	if outputPath != expected {
		t.Fatalf("expected %q, got %q", expected, outputPath)
	}

	info.Filename = "reports/summary.pdf"
	outputPath, err = resolveDownloadPath("", info)
	if err != nil {
		t.Fatalf("resolveDownloadPath: %v", err)
	}
	expected = filepath.Join(".", "summary.pdf")
	if outputPath != expected {
		t.Fatalf("expected %q, got %q", expected, outputPath)
	}

	outputPath, err = resolveDownloadPath("custom.bin", nil)
	if err != nil {
		t.Fatalf("resolveDownloadPath with explicit path: %v", err)
	}
	if outputPath != "custom.bin" {
		t.Fatalf("expected custom.bin, got %q", outputPath)
	}

	if _, err := resolveDownloadPath("", nil); err == nil {
		t.Fatal("expected error for missing file info")
	}
	if _, err := resolveDownloadPath("", &filesv1.FileInfo{Filename: " "}); err == nil {
		t.Fatal("expected error for empty filename")
	}
}

func TestParseExpiryDuration(t *testing.T) {
	duration, err := parseExpiryDuration("1h")
	if err != nil {
		t.Fatalf("parseExpiryDuration: %v", err)
	}
	if duration != time.Hour {
		t.Fatalf("expected 1h, got %s", duration)
	}

	if _, err := parseExpiryDuration("bad"); err == nil {
		t.Fatal("expected error for invalid duration")
	}
	if _, err := parseExpiryDuration(""); err == nil {
		t.Fatal("expected error for empty duration")
	}
}
