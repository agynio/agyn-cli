package local

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const caFileName = "agyn-local-ca.pem"

// CAInfo describes the extracted local CA.
type CAInfo struct {
	Subject     string `json:"subject" yaml:"subject"`
	NotBefore   string `json:"not_before" yaml:"not_before"`
	NotAfter    string `json:"not_after" yaml:"not_after"`
	Fingerprint string `json:"fingerprint_sha256" yaml:"fingerprint_sha256"`
	Path        string `json:"path" yaml:"path"`
	Trusted     bool   `json:"trusted" yaml:"trusted"`
}

// CAPath returns where the extracted CA is cached.
func CAPath() (string, error) {
	dir, err := CertsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, caFileName), nil
}

// EnsureCA extracts the CA from the running VM unless already cached.
func EnsureCA() (string, error) {
	path, err := CAPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return path, ExtractCA()
}

// ExtractCA reads the CA certificate from the running VM and caches it,
// overwriting any previous copy.
func ExtractCA() error {
	instance, err := GetInstance()
	if err != nil {
		return err
	}
	if !instance.Exists || instance.Status != "Running" {
		return fmt.Errorf("the VM must be running to extract the CA; run `agyn local start` first")
	}

	encoded, err := Shell("sudo", "kubectl", "-n", "istio-gateway",
		"get", "secret", "agyn-dev-ca", "-o", "jsonpath={.data.tls\\.crt}")
	if err != nil {
		return fmt.Errorf("read CA from VM: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return fmt.Errorf("decode CA: %w", err)
	}
	if _, err := parseCertificate(decoded); err != nil {
		return err
	}

	dir, err := CertsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create certs dir: %w", err)
	}

	path, err := CAPath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, decoded, 0o644); err != nil {
		return fmt.Errorf("write CA: %w", err)
	}
	return nil
}

// InspectCA returns details about the cached CA.
func InspectCA() (CAInfo, error) {
	path, err := CAPath()
	if err != nil {
		return CAInfo{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CAInfo{}, fmt.Errorf("no CA extracted yet; run `agyn local start` or `agyn local ca export` with the VM running")
		}
		return CAInfo{}, fmt.Errorf("read CA: %w", err)
	}

	cert, err := parseCertificate(data)
	if err != nil {
		return CAInfo{}, err
	}

	sum := sha256.Sum256(cert.Raw)
	return CAInfo{
		Subject:     cert.Subject.String(),
		NotBefore:   cert.NotBefore.Format(time.RFC3339),
		NotAfter:    cert.NotAfter.Format(time.RFC3339),
		Fingerprint: hex.EncodeToString(sum[:]),
		Path:        path,
		Trusted:     isTrusted(cert),
	}, nil
}

// InstallCA adds the cached CA to the system trust store. Requires sudo.
func InstallCA() error {
	path, err := CAPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no CA extracted yet; start the VM first")
	}

	switch runtime.GOOS {
	case "darwin":
		return runSudo("security", "add-trusted-cert", "-d", "-r", "trustRoot",
			"-k", "/Library/Keychains/System.keychain", path)
	case "linux":
		dest := "/usr/local/share/ca-certificates/agyn-local-ca.crt"
		if err := runSudo("cp", path, dest); err != nil {
			return err
		}
		return runSudo("update-ca-certificates")
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// UninstallCA removes the CA from the system trust store. Requires sudo.
func UninstallCA() error {
	path, err := CAPath()
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "darwin":
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read CA: %w", err)
		}
		cert, err := parseCertificate(data)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(cert.Raw)
		return runSudo("security", "delete-certificate",
			"-Z", strings.ToUpper(hex.EncodeToString(sum[:])),
			"/Library/Keychains/System.keychain")
	case "linux":
		if err := runSudo("rm", "-f", "/usr/local/share/ca-certificates/agyn-local-ca.crt"); err != nil {
			return err
		}
		return runSudo("update-ca-certificates", "--fresh")
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func parseCertificate(pemData []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("no certificate found in PEM data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	return cert, nil
}

func isTrusted(cert *x509.Certificate) bool {
	if runtime.GOOS != "darwin" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			return false
		}
		_, err = cert.Verify(x509.VerifyOptions{Roots: pool})
		return err == nil
	}

	// On macOS, ask the keychain about this exact certificate.
	tmp, err := os.CreateTemp("", "agyn-ca-*.pem")
	if err != nil {
		return false
	}
	defer os.Remove(tmp.Name())
	if err := pem.Encode(tmp, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}); err != nil {
		tmp.Close()
		return false
	}
	tmp.Close()

	return exec.Command("security", "verify-cert", "-c", tmp.Name()).Run() == nil
}

func runSudo(args ...string) error {
	cmd := exec.Command("sudo", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
