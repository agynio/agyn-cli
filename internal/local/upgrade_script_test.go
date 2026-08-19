package local

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The upgrade script is executable code that runs where nothing watches it, and
// `set -e` with `pipefail` makes every reader a place the whole run can die: a
// command substitution whose pipeline fails takes the script with it. Both
// readers fail routinely and harmlessly -- `helm status` errors for a release
// that is not installed, `helm show` needs a network -- and when the first of
// them did, a resume cleared the interrupted revision and then exited 1 with
// nothing to say.
func TestUpgradeScriptSurvivesReleasesThatAreNotInstalled(t *testing.T) {
	out, err := runUpgradeScriptWithStubs(t, map[string]string{
		// Neither release exists here: every `helm status` fails.
		"helm": `case "$1" in
			status) exit 1 ;;
			list) echo "[]" ;;
			show) echo "version: 0.52.0" ;;
			history) exit 1 ;;
			*) exit 0 ;;
		esac`,
		"kubectl": `case "$*" in
			*readyz*) echo ok ;;
			*) exit 0 ;;
		esac`,
	}, "--resume")

	if err != nil {
		t.Fatalf("expected the script to survive uninstalled releases, got %v:\n%s", err, out)
	}
	if strings.Contains(out, "AGYN|fail") {
		t.Fatalf("expected no reported failure:\n%s", out)
	}
}

// The reason --resume exists: an interrupted upgrade leaves a revision in
// flight, and it has to be cleared before anything else is attempted. It is
// cleared forward -- the record is dropped and the upgrade proceeds -- rather
// than rolled back onto workloads the interruption already moved.
func TestUpgradeScriptResumeClearsTheInterruptedRevisionThenUpgrades(t *testing.T) {
	out, err := runUpgradeScriptWithStubs(t, map[string]string{
		"helm": `case "$1" in
			status)
				[ "$2" = "agyn-platform" ] || exit 1
				echo '{"info":{"status":"pending-upgrade"},"name":"agyn-platform"}' ;;
			history) echo '[{"revision":1},{"revision":4}]' ;;
			list) echo '[{"chart":"agyn-platform-0.42.1"}]' ;;
			show) echo "version: 0.52.0" ;;
			upgrade) echo "upgraded" >&2 ;;
			*) exit 0 ;;
		esac`,
		"kubectl": `case "$*" in
			*readyz*) echo ok ;;
			*"delete secret"*) echo "$*" >> "$STUB_LOG" ;;
			*) exit 0 ;;
		esac`,
	}, "--resume")

	if err != nil {
		t.Fatalf("resume failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "AGYN|step|Clearing the interrupted upgrade") {
		t.Fatalf("expected the interrupted revision to be cleared:\n%s", out)
	}
	// The in-flight revision, not the one before it: dropping the wrong record
	// would discard the release's history of what is actually deployed.
	if log := stubLog(t); !strings.Contains(log, "sh.helm.release.v1.agyn-platform.v4") {
		t.Fatalf("expected revision 4's record to be deleted, got: %s", log)
	}
	if !strings.Contains(out, "AGYN|changed|") {
		t.Fatalf("expected the upgrade to proceed after clearing:\n%s", out)
	}
}

// An interrupted upgrade that is not being resumed has to say so, and say what
// continues it. Helm's own error names the lock and not the way out, and a
// release listing does not show the state at all.
func TestUpgradeScriptRefusesAPendingReleaseWithTheWayOut(t *testing.T) {
	out, _ := runUpgradeScriptWithStubs(t, map[string]string{
		"helm": `case "$1" in
			status)
				[ "$2" = "agyn-platform" ] || exit 1
				echo '{"info":{"status":"pending-upgrade"},"name":"agyn-platform"}' ;;
			*) exit 0 ;;
		esac`,
		"kubectl": `case "$*" in
			*readyz*) echo ok ;;
			*) exit 0 ;;
		esac`,
	})

	if !strings.Contains(out, "AGYN|fail|") || !strings.Contains(out, "--resume") {
		t.Fatalf("expected a failure naming the recovery command:\n%s", out)
	}
}

// runUpgradeScriptWithStubs runs the embedded script against stub tools, so the
// shell itself is exercised without a VM.
func runUpgradeScriptWithStubs(t *testing.T, stubs map[string]string, args ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range stubs {
		script := "#!/usr/bin/env bash\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}

	cmd := exec.Command("bash", append([]string{"-s", "--"}, args...)...)
	cmd.Stdin = strings.NewReader(upgradeScript)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_LOG="+filepath.Join(dir, "calls.log"))
	stubDir = dir

	output, err := cmd.CombinedOutput()
	return string(output), err
}

// stubDir is where the last stub run recorded its calls.
var stubDir string

func stubLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stubDir, "calls.log"))
	if err != nil {
		return ""
	}
	return string(data)
}

// A failed upgrade still records the version it was reaching for, and
// `helm list` reports that version as the installed one. Read that way, a
// release left failed at the target answers "already at" and is never
// repaired -- the upgrade that would fix it is the one being skipped. What is
// installed is the newest revision that actually deployed.
func TestUpgradeScriptRetriesAReleaseLeftFailedAtTheTargetVersion(t *testing.T) {
	out, err := runUpgradeScriptWithStubs(t, map[string]string{
		"helm": `case "$1" in
			status) echo '{"info":{"status":"failed"}}' ;;
			list) echo '[{"name":"agyn-platform","chart":"agyn-platform-0.70.3"}]' ;;
			history) echo '[{"revision":23,"status":"superseded","chart":"agyn-platform-0.70.1"},{"revision":24,"status":"deployed","chart":"agyn-platform-0.70.2"},{"revision":25,"status":"failed","chart":"agyn-platform-0.70.3"}]' ;;
			show) echo "version: 0.70.3" ;;
			upgrade) echo "upgraded" ;;
			*) exit 0 ;;
		esac`,
		"kubectl": `case "$*" in
			*readyz*) echo ok ;;
			*) exit 0 ;;
		esac`,
	})

	if err != nil {
		t.Fatalf("expected the script to succeed, got %v:\n%s", err, out)
	}
	if strings.Contains(out, "already at 0.70.3") {
		t.Fatalf("a release left failed at the target was treated as installed:\n%s", out)
	}
	if !strings.Contains(out, "0.70.2 → 0.70.3") {
		t.Fatalf("expected an upgrade from the deployed version, got:\n%s", out)
	}
}
