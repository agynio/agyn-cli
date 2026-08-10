package local

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agynio/agyn-cli/internal/terminal"
)

// The whole point of the marker protocol: Helm's admission warnings, kubectl's
// klog lines and the release listing answer a different question than the one
// the user asked, so they go to the log and the steps go to the terminal.
func TestUpgradeRendererSeparatesStepsFromToolOutput(t *testing.T) {
	var out, log bytes.Buffer
	renderer := &upgradeRenderer{steps: terminal.NewPlainSteps(&out), log: &log}

	renderer.consume(strings.NewReader(strings.Join([]string{
		`AGYN|note|services running from source (devspace) will be reset to their chart images`,
		`AGYN|step|agyn-platform|0.51.0 → 0.52.0`,
		`I0810 11:12:21.609992  489181 warnings.go:110] "Warning: configured AuthorizationPolicy will deny all traffic"`,
		`Release "agyn-platform" has been upgraded. Happy Helming!`,
		`AGYN|done|0.51.0 → 0.52.0`,
		`AGYN|step|agyn-apps|`,
		`AGYN|skip|already at 0.19.0`,
	}, "\n")))
	renderer.finish()

	rendered := out.String()
	for _, want := range []string{"agyn-platform", "0.51.0 → 0.52.0", "agyn-apps", "already at 0.19.0"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected %q on the terminal:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"warnings.go", "Happy Helming", "AGYN|"} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("expected %q to stay out of the terminal:\n%s", unwanted, rendered)
		}
	}
	for _, want := range []string{"warnings.go", "Happy Helming"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("expected %q in the log:\n%s", want, log.String())
		}
	}
}

// An upgrade whose release is already current must read as "nothing to do",
// not as a line that looks exactly like the one that replaced every workload.
func TestUpgradeRendererMarksAnAlreadyCurrentReleaseAsSkipped(t *testing.T) {
	var out, log bytes.Buffer
	renderer := &upgradeRenderer{steps: terminal.NewPlainSteps(&out), log: &log}

	renderer.consume(strings.NewReader("AGYN|step|agyn-platform|\nAGYN|skip|already at 0.52.0\n"))
	renderer.finish()

	if !strings.Contains(out.String(), "already at 0.52.0") {
		t.Fatalf("expected the skip reason, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "→") {
		t.Fatalf("expected no version transition for an untouched release:\n%s", out.String())
	}
}

// A step the script leaves open — the last one before the process exits — still
// has to be closed, or the run ends on a line that reads as still running.
func TestUpgradeRendererClosesATrailingStep(t *testing.T) {
	var out, log bytes.Buffer
	renderer := &upgradeRenderer{steps: terminal.NewPlainSteps(&out), log: &log}

	renderer.consume(strings.NewReader("AGYN|step|Restoring the browser-facing port|\n"))
	renderer.finish()

	if !strings.Contains(out.String(), "Restoring the browser-facing port") {
		t.Fatalf("expected the trailing step to be closed:\n%s", out.String())
	}
}
