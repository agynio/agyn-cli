package terminal

import (
	"bytes"
	"strings"
	"testing"
)

// Piping either command to a file has to yield a readable list of steps: a
// CI log full of carriage returns and cursor escapes is worse than no
// progress reporting at all.
func TestStepsPrintOnceWithoutControlCharactersWhenNotATerminal(t *testing.T) {
	var buf bytes.Buffer
	steps := NewSteps(&buf)

	step := steps.Start("Downloading image 0.3.0")
	step.Detail("1.2 GB of 4.6 GB")
	step.Detail("2.4 GB of 4.6 GB")
	step.Done("arm64")
	steps.Start("Trusting the CA").Skip("skipped; install later with: agyn local ca install")

	got := buf.String()
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("expected no control characters in:\n%q", got)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected one line per step, got %d:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], symbolDone) || !strings.Contains(lines[0], "arm64") {
		t.Fatalf("expected the finished step to carry its detail: %q", lines[0])
	}
	// The intermediate details are progress, not history: only the last stands.
	if strings.Contains(got, "1.2 GB") {
		t.Fatalf("expected superseded progress not to be printed:\n%s", got)
	}
	if !strings.Contains(lines[1], symbolSkip) {
		t.Fatalf("expected a skipped step to be marked as such: %q", lines[1])
	}
}

// A failed step is the last thing a command does, and the error belongs on its
// line rather than after one that still reads as running.
func TestFailMarksTheStepAndReturnsTheError(t *testing.T) {
	var buf bytes.Buffer
	steps := NewSteps(&buf)

	step := steps.Start("Waiting for the platform")
	err := step.Fail(errTest)
	if err != errTest {
		t.Fatalf("expected Fail to return the error it was given, got %v", err)
	}
	if !strings.Contains(buf.String(), symbolFail) || !strings.Contains(buf.String(), errTest.Error()) {
		t.Fatalf("expected the failure on the step's line:\n%s", buf.String())
	}
}

// Detail arriving from a poller after the step closed must not reopen it.
func TestDetailAfterDoneIsIgnored(t *testing.T) {
	var buf bytes.Buffer
	steps := NewSteps(&buf)

	step := steps.Start("Upgrading")
	step.Done("0.51.0 → 0.52.0")
	step.Detail("waiting on gateway")

	if strings.Contains(buf.String(), "waiting on gateway") {
		t.Fatalf("expected a late detail to be dropped:\n%s", buf.String())
	}
}

// A finished start ends in exactly one link, and it is the last thing printed.
func TestCallToActionIsOneLine(t *testing.T) {
	var buf bytes.Buffer
	steps := NewSteps(&buf)
	steps.CallToAction("Open the console", "https://console.agyn.dev:2496")

	lines := strings.Split(strings.Trim(buf.String(), "\n"), "\n")
	if len(lines) != 1 || strings.Count(lines[0], "https://") != 1 {
		t.Fatalf("expected a single link, got:\n%s", buf.String())
	}
}

// An instantly-reported step has no duration to show. Constructed without a
// start time it had one anyway, measured from the zero time.
func TestInstantStepsShowNoDuration(t *testing.T) {
	var buf bytes.Buffer
	steps := NewSteps(&buf)
	steps.Report("Trusting the CA", "installed in the system trust store")
	steps.Skipped("Image 0.3.0", "already downloaded")

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.Contains(line, "m") && strings.Contains(line, "s  ") {
			t.Errorf("expected no elapsed time on an instant step: %q", line)
		}
		if strings.Contains(line, "1537") {
			t.Errorf("expected no duration measured from the zero time: %q", line)
		}
	}
}

func TestTruncateCountsPrintableColumnsOnly(t *testing.T) {
	line := "\x1b[36m⠋\x1b[0m Downloading"
	if got := truncate(line, 40); got != line {
		t.Fatalf("expected a short line to survive, got %q", got)
	}
	got := truncate(line, 4)
	if strings.Contains(got, "Downloading") {
		t.Fatalf("expected truncation past the width, got %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("expected truncation to reset the styling, got %q", got)
	}
}

var errTest = testError("no route to host")

type testError string

func (e testError) Error() string { return string(e) }
