package local

import (
	"os/exec"
	"strings"
)

// Dependency describes one host tool required by `agyn local`.
type Dependency struct {
	Name     string `json:"name" yaml:"name"`
	Found    bool   `json:"found" yaml:"found"`
	Version  string `json:"version,omitempty" yaml:"version,omitempty"`
	Fix      string `json:"fix,omitempty" yaml:"fix,omitempty"`
	Optional bool   `json:"optional" yaml:"optional"`
}

// CheckDependencies inspects the host for the tools `agyn local` needs.
func CheckDependencies() []Dependency {
	return []Dependency{
		checkTool("limactl", []string{"--version"}, "brew install lima", false),
		checkTool("xz", []string{"--version"}, "brew install xz", false),
		checkTool("qemu-system-"+qemuArch(), []string{"--version"}, "brew install qemu", false),
	}
}

// MissingRequired returns the required dependencies that were not found.
func MissingRequired(deps []Dependency) []Dependency {
	var missing []Dependency
	for _, dep := range deps {
		if !dep.Found && !dep.Optional {
			missing = append(missing, dep)
		}
	}
	return missing
}

func checkTool(name string, versionArgs []string, fix string, optional bool) Dependency {
	dep := Dependency{Name: name, Fix: fix, Optional: optional}

	path, err := exec.LookPath(name)
	if err != nil {
		return dep
	}
	dep.Found = true

	out, err := exec.Command(path, versionArgs...).Output()
	if err == nil {
		line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
		dep.Version = strings.TrimSpace(line)
	}
	return dep
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
