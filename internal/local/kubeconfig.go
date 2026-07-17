package local

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// KubeContextName is the kubeconfig context/cluster/user name for the VM.
const KubeContextName = "agyn-local"

// FetchKubeconfig reads the k3s kubeconfig from the running VM and rewrites it
// to be usable from the host: server URL pointing at the forwarded API port
// and all entries named after the agyn-local context. k3s includes 127.0.0.1
// in the API server certificate SANs, so TLS verification keeps working.
func FetchKubeconfig(apiPort int) (map[string]any, error) {
	raw, err := Shell("sudo", "cat", "/etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig from VM: %w", err)
	}

	var kubeconfig map[string]any
	if err := yaml.Unmarshal([]byte(raw), &kubeconfig); err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}

	rename := func(key, nested string) error {
		entries, ok := kubeconfig[key].([]any)
		if !ok || len(entries) == 0 {
			return fmt.Errorf("kubeconfig has no %s", key)
		}
		entry, ok := entries[0].(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected %s format", key)
		}
		entry["name"] = KubeContextName
		if nested != "" {
			if inner, ok := entry[nested].(map[string]any); ok {
				if key == "contexts" {
					inner["cluster"] = KubeContextName
					inner["user"] = KubeContextName
				}
				if key == "clusters" {
					inner["server"] = fmt.Sprintf("https://127.0.0.1:%d", apiPort)
				}
			}
		}
		return nil
	}

	if err := rename("clusters", "cluster"); err != nil {
		return nil, err
	}
	if err := rename("contexts", "context"); err != nil {
		return nil, err
	}
	if err := rename("users", "user"); err != nil {
		return nil, err
	}
	kubeconfig["current-context"] = KubeContextName

	return kubeconfig, nil
}

// CheckAPIPort verifies the forwarded k3s API answers TLS on the host port.
// VMs created before the API forward existed need to be recreated to get it.
func CheckAPIPort(apiPort int) error {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", fmt.Sprintf("127.0.0.1:%d", apiPort),
		&tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return fmt.Errorf("kubernetes API not reachable on 127.0.0.1:%d: %w", apiPort, err)
	}
	conn.Close()
	return nil
}

// MergeKubeconfig upserts the agyn-local cluster/context/user into the
// kubeconfig at path (typically ~/.kube/config), preserving everything else.
// current-context is not changed.
func MergeKubeconfig(path string, fetched map[string]any) error {
	target := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &target); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if target["apiVersion"] == nil {
		target["apiVersion"] = "v1"
		target["kind"] = "Config"
	}

	for _, key := range []string{"clusters", "contexts", "users"} {
		existing, _ := target[key].([]any)
		kept := make([]any, 0, len(existing)+1)
		for _, entry := range existing {
			if m, ok := entry.(map[string]any); ok && m["name"] == KubeContextName {
				continue
			}
			kept = append(kept, entry)
		}
		fetchedEntries, _ := fetched[key].([]any)
		kept = append(kept, fetchedEntries...)
		target[key] = kept
	}

	if target["current-context"] == nil || target["current-context"] == "" {
		target["current-context"] = KubeContextName
	}

	data, err := yaml.Marshal(target)
	if err != nil {
		return fmt.Errorf("encode kubeconfig: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	// Write via a temp file in the same directory so an interrupted write
	// cannot corrupt the user's kubeconfig.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agyn-kubeconfig-*")
	if err != nil {
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("chmod kubeconfig: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}
