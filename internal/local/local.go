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
	// InstanceName is the single Lima instance managed by `agyn local`.
	InstanceName = "agyn"

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

// CertsDir returns the directory for the extracted local CA.
func CertsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "certs"), nil
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
