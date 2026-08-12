package terminal

import (
	"os"
	"testing"
)

// Not an assertion: prints the block a finished start ends with, so its shape
// can be looked at. `go test ./internal/terminal -run Preview -v`.
func TestPreviewFinishedStart(t *testing.T) {
	s := NewSteps(os.Stdout)
	s.Report("Configuring profile local", "organization 0c5ed30b")
	s.Rule()
	s.CallToAction("Open the console", "https://console.agyn.dev:2497")
	os.Stdout.WriteString("\n")
	s.Detail("Regular user (recommended)", "user@agyn.dev / user")
	s.Detail("Cluster admin", "admin@agyn.dev / admin")
}
