// Package local manages the local Agyn platform VM: downloading prebuilt
// images from the CDN, driving Lima, and handling the local CA.
package local

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// DefaultInstanceName is the VM `agyn local` acts on when none is named.
	DefaultInstanceName = "agyn"

	// DefaultBaseURL serves the published platform VM images.
	DefaultBaseURL = "https://downloads.agyn.cloud/bundle-vm"

	// BaseURLEnv overrides the download endpoint.
	BaseURLEnv = "AGYN_LOCAL_BASE_URL"

	// BaseDomain is the wildcard domain routed to the VM ingress; it resolves
	// to 127.0.0.1 publicly.
	BaseDomain = "agyn.dev"

	// IngressNodePort is the guest NodePort the Istio ingress gateway listens
	// on inside the VM; the configured local port forwards to it.
	IngressNodePort = 32443
)

// BaseURL returns the CDN endpoint, honoring the environment override.
func BaseURL() string {
	if v := os.Getenv(BaseURLEnv); v != "" {
		return v
	}
	return DefaultBaseURL
}

// Arch maps the host architecture to the published image architecture.
func Arch() (string, error) {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64", nil
	case "amd64":
		return "amd64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

// selected is the VM this process acts on.
//
// One invocation of the CLI acts on exactly one VM, resolved once from
// --instance or the stored selection before any command body runs. Holding it
// here rather than threading a name through every call keeps the ordinary
// single-VM case free of an argument that would always be the same value.
var selected = DefaultInstanceName

// Use fixes the VM this process acts on. Called once, from the command layer.
func Use(name string) {
	if name != "" {
		selected = name
	}
}

// InstanceName is the Lima instance this process acts on.
func InstanceName() string { return selected }

// Dir returns ~/.agyn/local, the root for everything `agyn local` stores.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".agyn", "local"), nil
}

// ImageDir returns the directory holding a downloaded image version.
func ImageDir(version, arch string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "images", version, arch), nil
}

// CertsDir returns the directory holding this VM's extracted CA. Each VM signs
// with its own CA, so they cannot share a file. The default VM keeps the
// original path, leaving existing installs where they are.
func CertsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if selected == DefaultInstanceName {
		return filepath.Join(dir, "certs"), nil
	}
	return filepath.Join(dir, "certs", selected), nil
}

// LimaHome returns the LIMA_HOME used for the managed instance so all VM
// state lives under ~/.agyn/local.
func LimaHome() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lima"), nil
}

// DiskFileName returns the qcow2 file name for an architecture.
func DiskFileName(arch string) string {
	return fmt.Sprintf("bundle-vm-platform-%s.qcow2", arch)
}
