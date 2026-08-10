package local

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// CheckState is the outcome of one preflight check.
type CheckState string

const (
	// CheckOK is a check that passed.
	CheckOK CheckState = "ok"
	// CheckMissing is a tool that is not installed.
	CheckMissing CheckState = "missing"
	// CheckOutdated is a tool that is installed but older than the minimum.
	CheckOutdated CheckState = "outdated"
	// CheckFailed is a non-tool condition that is not satisfied.
	CheckFailed CheckState = "failed"
	// CheckWarning is worth reporting and not worth refusing to start over.
	CheckWarning CheckState = "warning"
)

// Check is one preflight result.
type Check struct {
	Name     string     `json:"name" yaml:"name"`
	State    CheckState `json:"state" yaml:"state"`
	Detail   string     `json:"detail,omitempty" yaml:"detail,omitempty"`
	Fix      string     `json:"fix,omitempty" yaml:"fix,omitempty"`
	Blocking bool       `json:"blocking" yaml:"blocking"`

	// tool is set when the fix is installing a host tool, which is the only
	// fix `--install-deps` and `doctor --fix` know how to apply.
	tool *Tool
}

// Passed reports whether a check is one that lets a start proceed.
func (c Check) Passed() bool { return c.State == CheckOK || c.State == CheckWarning }

// Tool reports whether this check is a host tool, which is the only kind of
// failure `--install-deps` and `doctor --fix` can act on.
func (c Check) Tool() bool { return c.tool != nil }

// PreflightOptions narrow the checks to what a particular command needs.
type PreflightOptions struct {
	// Ports must be free. Only a VM being created needs this: an existing VM
	// already holds its own ports, and finding them taken by itself would
	// refuse every restart.
	Ports []int
	// Space checks room for an image and the VM created from it.
	Space bool
}

// Disk space needed under ~/.agyn/local for a first start: the compressed
// download (~1.4 GB) plus the disk it decompresses to (~4.6 GB) plus the copy
// Lima takes into the instance, and then room for the VM to grow. Below the
// minimum a start fails partway through decompression, having already spent the
// download; the recommendation is where it stops being tight.
const (
	minFreeBytes         = 12 << 30
	recommendedFreeBytes = 20 << 30
)

// Preflight reports what would stop this host from running the platform VM.
//
// It runs on every `agyn local start` and is all of `agyn local doctor`: a
// missing or too-old host tool is the most common reason a first run fails, and
// left to limactl the failure arrives as a tool error rather than a sentence
// naming what to install.
func Preflight(opts PreflightOptions) []Check {
	checks := make([]Check, 0, 8)
	for _, tool := range RequiredTools() {
		checks = append(checks, checkTool(tool))
	}
	if opts.Space {
		checks = append(checks, checkFreeSpace())
	}
	for _, port := range opts.Ports {
		checks = append(checks, checkPort(port))
	}
	checks = append(checks, checkVirtualization())
	return checks
}

// BlockingFailures returns the checks that stop a start.
func BlockingFailures(checks []Check) []Check {
	var failures []Check
	for _, check := range checks {
		if !check.Passed() && check.Blocking {
			failures = append(failures, check)
		}
	}
	return failures
}

// InstallableTools returns the tools behind failing checks — what
// `--install-deps` and `doctor --fix` would install.
func InstallableTools(checks []Check) []Tool {
	var tools []Tool
	for _, check := range checks {
		if !check.Passed() && check.tool != nil {
			tools = append(tools, *check.tool)
		}
	}
	return tools
}

func checkTool(tool Tool) Check {
	check := Check{Name: tool.Label, Blocking: true, Fix: tool.fix(), tool: &tool}

	found, version := tool.inspect()
	switch {
	case !found:
		check.State = CheckMissing
		check.Detail = "not installed"
	case compareVersions(version, tool.MinVersion) < 0:
		check.State = CheckOutdated
		check.Detail = fmt.Sprintf("%s, needs %s or newer", version, tool.MinVersion)
	default:
		check.State = CheckOK
		check.Detail = version
		check.Fix = ""
	}
	return check
}

func checkFreeSpace() Check {
	check := Check{Name: "disk space", Blocking: true}

	dir, err := Dir()
	if err != nil {
		check.State = CheckWarning
		check.Detail = err.Error()
		return check
	}
	free, err := freeSpace(dir)
	if err != nil {
		// Unmeasurable is not the same as insufficient: report it and let the
		// start proceed rather than refuse on a failed syscall.
		check.State = CheckWarning
		check.Detail = err.Error()
		return check
	}

	check.Detail = fmt.Sprintf("%s free in %s", formatBytes(free), tildePath(dir))
	switch {
	case free < minFreeBytes:
		check.State = CheckFailed
		check.Detail = fmt.Sprintf("%s free in %s, needs %s",
			formatBytes(free), tildePath(dir), formatBytes(minFreeBytes))
		check.Fix = "free up disk space"
	case free < recommendedFreeBytes:
		check.State = CheckWarning
		check.Detail += fmt.Sprintf(" (%s recommended)", formatBytes(recommendedFreeBytes))
	default:
		check.State = CheckOK
	}
	return check
}

// freeSpace reports the bytes available to this user at path, or at the nearest
// existing ancestor — nothing under ~/.agyn/local exists before a first start.
func freeSpace(path string) (int64, error) {
	for {
		var stat unix.Statfs_t
		err := unix.Statfs(path, &stat)
		if err == nil {
			return int64(stat.Bavail) * int64(stat.Bsize), nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, fmt.Errorf("measure free space: %w", err)
		}
		path = parent
	}
}

func checkPort(port int) Check {
	check := Check{Name: fmt.Sprintf("port %d", port), Blocking: true}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		check.State = CheckFailed
		check.Detail = "already in use"
		check.Fix = fmt.Sprintf("choose another with --port, or: agyn local config set port <n>")
		return check
	}
	listener.Close()
	check.State = CheckOK
	check.Detail = "free"
	return check
}

func checkVirtualization() Check {
	check := Check{Name: "virtualization", Blocking: true}
	available, detail := kvmAvailable()
	if available {
		check.State = CheckOK
		check.Detail = "available"
		return check
	}
	check.State = CheckFailed
	check.Detail = detail
	return check
}

func formatBytes(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%d MB", bytes>>20)
	default:
		return fmt.Sprintf("%d KB", bytes>>10)
	}
}

// tildePath shortens a path under the home directory, which is where
// everything this CLI writes lives.
func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		return path
	}
	return "~/" + filepath.ToSlash(rel)
}
