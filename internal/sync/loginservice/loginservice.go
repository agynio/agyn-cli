// Package loginservice registers a user-level service so sync sessions resume
// at login.
//
// This is opt-in and nothing is installed otherwise. Nothing resumes
// automatically after a reboot by default: a session that silently came back
// and started reconciling files an engineer had forgotten about is worse than
// one reported as not running.
package loginservice

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Label names the service on both platforms. Reverse-DNS is the macOS
// convention and harmless on Linux.
const Label = "io.agyn.sync"

// ErrUnsupported reports a platform with no user-level service manager we
// target. The CLI ships darwin and linux only.
var ErrUnsupported = errors.New("resume-at-login is not available on this platform")

// run invokes the platform's service manager. It is a variable so tests can
// stub it: writing a plist into a temporary HOME is harmless, but loading it
// registers a real agent in the developer's launchd that outlives the test.
var run = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// Install registers the service. It is idempotent: installing twice leaves one
// registration.
func Install(executable string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(executable)
	case "linux":
		return installSystemdUnit(executable)
	default:
		return "", ErrUnsupported
	}
}

// Uninstall removes the registration. A missing one is not an error: the
// command's job is to leave nothing installed.
func Uninstall() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchAgent()
	case "linux":
		return uninstallSystemdUnit()
	default:
		return "", ErrUnsupported
	}
}

// Status reports where the registration lives, or empty when none is
// installed.
func Status() (string, bool) {
	path, err := unitPath()
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return path, false
	}
	return path, true
}

func unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "agyn-sync.service"), nil
	default:
		return "", ErrUnsupported
	}
}

// resumeArgs is what the service runs. `sync resume-all` starts every persisted
// session; it is the only command that does, so a login item cannot be repurposed
// into running something else.
func resumeArgs() []string { return []string{"sandbox", "sync", "resume-all"} }

func installLaunchAgent(executable string) (string, error) {
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	arguments := append([]string{executable}, resumeArgs()...)
	var program strings.Builder
	for _, argument := range arguments {
		fmt.Fprintf(&program, "    <string>%s</string>\n", argument)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
%s  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
`, Label, program.String())
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return "", err
	}
	// Best effort: a plist in place takes effect at the next login regardless,
	// so a load failure is not worth failing the command over.
	_ = run("launchctl", "unload", path)
	_ = run("launchctl", "load", path)
	return path, nil
}

func uninstallLaunchAgent() (string, error) {
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	_ = run("launchctl", "unload", path)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func installSystemdUnit(executable string) (string, error) {
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	unit := fmt.Sprintf(`[Unit]
Description=agyn sandbox workspace sync
After=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s %s

[Install]
WantedBy=default.target
`, executable, strings.Join(resumeArgs(), " "))
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return "", err
	}
	_ = run("systemctl", "--user", "daemon-reload")
	_ = run("systemctl", "--user", "enable", "agyn-sync.service")
	return path, nil
}

func uninstallSystemdUnit() (string, error) {
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	_ = run("systemctl", "--user", "disable", "agyn-sync.service")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	_ = run("systemctl", "--user", "daemon-reload")
	return path, nil
}
