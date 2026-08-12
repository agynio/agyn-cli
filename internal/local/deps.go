package local

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Tool is one host tool `agyn local` needs, and what it takes to get it.
type Tool struct {
	// Name is the binary as it must appear on PATH.
	Name string `json:"name" yaml:"name"`
	// Label is what a person calls it, when that differs from the binary.
	Label string `json:"label" yaml:"label"`
	// MinVersion is the oldest release the published lima.yaml and this CLI's
	// use of the tool are known to work with.
	MinVersion string `json:"minVersion,omitempty" yaml:"minVersion,omitempty"`

	// packages names the tool per package manager. A manager absent from the
	// map does not package it — lima, for one, is packaged by no distribution.
	packages map[string]string
	// manual is what to tell someone no package manager can help.
	manual string
}

// RequiredTools is what `agyn local start` cannot proceed without.
//
// Minimums are the point of the version field: a tool that is present but too
// old passes a presence check and fails later inside limactl, in a message that
// names neither the tool's version nor the fix.
func RequiredTools() []Tool {
	return []Tool{
		{
			Name:       "limactl",
			Label:      "lima",
			MinVersion: "1.0.0",
			packages:   map[string]string{"brew": "lima"},
			manual:     "see https://lima-vm.io/docs/installation/",
		},
		{
			Name:       "xz",
			Label:      "xz",
			MinVersion: "5.0.0",
			packages:   map[string]string{"brew": "xz", "apt": "xz-utils", "dnf": "xz", "pacman": "xz"},
		},
		{
			Name:       "qemu-system-" + qemuArch(),
			Label:      "qemu",
			MinVersion: "7.0.0",
			packages:   map[string]string{"brew": "qemu", "apt": "qemu-system", "dnf": "qemu-system-" + qemuArch(), "pacman": "qemu-base"},
		},
	}
}

// Installer is the package manager this host has, if it has one.
type Installer struct {
	// Key selects package names from a tool's map.
	Key string
	// Name is what the user calls it.
	Name string
	// command builds the shell command that installs the given packages.
	command func(packages []string) string
}

// installers are tried in order. Homebrew comes first on either platform: it is
// the only one of these that packages lima, so a machine with brew can be
// fixed in one command whichever OS it runs.
var installers = []Installer{
	{Key: "brew", Name: "Homebrew", command: func(p []string) string {
		return "brew install " + strings.Join(p, " ")
	}},
	{Key: "apt", Name: "apt", command: func(p []string) string {
		return "sudo apt-get update && sudo apt-get install -y " + strings.Join(p, " ")
	}},
	{Key: "dnf", Name: "dnf", command: func(p []string) string {
		return "sudo dnf install -y " + strings.Join(p, " ")
	}},
	{Key: "pacman", Name: "pacman", command: func(p []string) string {
		return "sudo pacman -S --needed --noconfirm " + strings.Join(p, " ")
	}},
}

// installerBinaries maps an installer to the binary that proves it is present.
var installerBinaries = map[string]string{
	"brew": "brew", "apt": "apt-get", "dnf": "dnf", "pacman": "pacman",
}

// DetectInstaller returns the package manager on this host, or nil.
//
// The CLI never installs a package manager: bootstrapping Homebrew is a
// several-minute, sudo-taking, machine-wide change that a platform CLI has no
// business making on someone's behalf. A machine without one is told what to
// install and by whom.
func DetectInstaller() *Installer {
	for index := range installers {
		if _, err := exec.LookPath(installerBinaries[installers[index].Key]); err == nil {
			return &installers[index]
		}
	}
	return nil
}

// InstallCommand is the command that would install these tools, and whether the
// detected installer can install all of them. A tool it does not package — lima
// outside Homebrew — leaves the caller to print that tool's own instruction.
func InstallCommand(tools []Tool) (string, bool) {
	installer := DetectInstaller()
	if installer == nil {
		return "", false
	}
	packages := make([]string, 0, len(tools))
	for _, tool := range tools {
		name, ok := tool.packages[installer.Key]
		if !ok {
			return "", false
		}
		packages = append(packages, name)
	}
	if len(packages) == 0 {
		return "", false
	}
	return installer.command(packages), true
}

// InstallTools runs the install command with the terminal attached, so brew
// reports its own progress and sudo can ask for a password.
func InstallTools(tools []Tool, stdin io.Reader, stdout, stderr io.Writer) error {
	command, ok := InstallCommand(tools)
	if !ok {
		return fmt.Errorf("no package manager on this host installs %s", toolNames(tools))
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}

func toolNames(tools []Tool) string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Label)
	}
	return strings.Join(names, ", ")
}

// fix describes how to get this tool, whether or not a package manager can.
//
// With no package manager on the host there is still something useful to say:
// the package name under the manager most likely to be reachable, so the user
// can map it to whatever theirs is. "install qemu" on its own is not a fix.
func (t Tool) fix() string {
	if command, ok := InstallCommand([]Tool{t}); ok {
		return command
	}
	if name, ok := t.packages["brew"]; ok {
		return "brew install " + name + " (or your platform's equivalent)"
	}
	if t.manual != "" {
		return t.manual
	}
	return "install " + t.Label
}

// inspect reports what the host has of this tool.
func (t Tool) inspect() (found bool, version string) {
	path, err := exec.LookPath(t.Name)
	if err != nil {
		return false, ""
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		// Present but unable to report a version: the presence is what the
		// check is for, and an unreadable version is not grounds to refuse.
		return true, ""
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return true, parseVersion(line)
}

var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

// parseVersion pulls the version out of a --version line. Every tool here
// prints it differently ("limactl version 1.0.4", "xz (XZ Utils) 5.6.2",
// "QEMU emulator version 9.1.0") and all three put it in the first line.
func parseVersion(line string) string {
	return versionPattern.FindString(line)
}

// compareVersions orders two dotted numeric versions. An unparseable or empty
// version compares equal, so a tool that would not say what it is is given the
// benefit of the doubt rather than blocked.
func compareVersions(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) || i < len(right); i++ {
		var l, r int
		if i < len(left) {
			l, _ = strconv.Atoi(left[i])
		}
		if i < len(right) {
			r, _ = strconv.Atoi(right[i])
		}
		if l != r {
			if l < r {
				return -1
			}
			return 1
		}
	}
	return 0
}

func qemuArch() string {
	arch, err := Arch()
	if err != nil {
		return "aarch64"
	}
	if arch == "amd64" {
		return "x86_64"
	}
	return "aarch64"
}

// kvmAvailable reports whether hardware virtualization is usable here.
//
// Linux only: macOS reaches the Hypervisor framework without a device node or a
// group membership. A Linux host without /dev/kvm runs the VM under emulation,
// where a k3s cluster with Istio in it does not finish starting in any time a
// person would wait — so this is a blocking condition, not a slow one.
func kvmAvailable() (bool, string) {
	if runtime.GOOS != "linux" {
		return true, ""
	}
	file, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err == nil {
		file.Close()
		return true, ""
	}
	if os.IsNotExist(err) {
		return false, "/dev/kvm is missing; enable virtualization in the BIOS or use a host that exposes it"
	}
	return false, "/dev/kvm is not writable by this user; run: sudo usermod -aG kvm $USER, then log in again"
}
