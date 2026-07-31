package local

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Instance describes the managed Lima instance.
type Instance struct {
	Exists bool   `json:"exists" yaml:"exists"`
	Status string `json:"status,omitempty" yaml:"status,omitempty"`
	Dir    string `json:"dir,omitempty" yaml:"dir,omitempty"`
}

// VMOptions parametrize instance creation.
type VMOptions struct {
	Port    int
	APIPort int
	CPUs    int
	Memory  string
}

func limaEnv() ([]string, error) {
	limaHome, err := LimaHome()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(limaHome, 0o755); err != nil {
		return nil, fmt.Errorf("create lima home: %w", err)
	}
	return append(os.Environ(), "LIMA_HOME="+limaHome), nil
}

func limactl(stdout, stderr io.Writer, args ...string) error {
	env, err := limaEnv()
	if err != nil {
		return err
	}
	cmd := exec.Command("limactl", args...)
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// GetInstance reports the managed instance's state.
func GetInstance() (Instance, error) {
	var buf bytes.Buffer
	var errBuf bytes.Buffer
	if err := limactl(&buf, &errBuf, "list", "--json"); err != nil {
		return Instance{}, fmt.Errorf("limactl list: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}

	decoder := json.NewDecoder(&buf)
	for {
		var entry struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Dir    string `json:"dir"`
		}
		if err := decoder.Decode(&entry); err != nil {
			if err == io.EOF {
				break
			}
			return Instance{}, fmt.Errorf("parse limactl list output: %w", err)
		}
		if entry.Name == InstanceName() {
			return Instance{Exists: true, Status: entry.Status, Dir: entry.Dir}, nil
		}
	}
	return Instance{}, nil
}

// CreateAndStart creates the instance from a downloaded image directory and
// boots it. The image's lima.yaml template is adjusted with the requested
// options before use.
func CreateAndStart(imageDir string, opts VMOptions, stdout, stderr io.Writer) error {
	rendered, err := renderLimaConfig(imageDir, opts)
	if err != nil {
		return err
	}
	if err := limactl(stdout, stderr, "start", "--name", InstanceName(), rendered); err != nil {
		// A failed creation leaves a half-registered instance behind that
		// would shadow the next attempt; remove it so retries start clean.
		_ = limactl(io.Discard, io.Discard, "delete", "--force", InstanceName())
		return err
	}
	return nil
}

// Start boots an existing instance.
func Start(stdout, stderr io.Writer) error {
	return limactl(stdout, stderr, "start", InstanceName())
}

// Stop shuts the instance down.
func Stop(stdout, stderr io.Writer) error {
	return limactl(stdout, stderr, "stop", InstanceName())
}

// Delete removes the instance and its disk state.
func Delete(stdout, stderr io.Writer) error {
	return limactl(stdout, stderr, "delete", "--force", InstanceName())
}

// Shell runs a command inside the guest and returns its stdout.
func Shell(args ...string) (string, error) {
	env, err := limaEnv()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("limactl", append([]string{"shell", InstanceName(), "--"}, args...)...)
	cmd.Env = env
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("limactl shell: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

// renderLimaConfig copies the published lima.yaml next to the disk, applying
// the configured port/cpu/memory. The published template references the disk
// by relative path, so the rendered copy lives in the image directory.
func renderLimaConfig(imageDir string, opts VMOptions) (string, error) {
	source := filepath.Join(imageDir, "lima.yaml")
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read lima template: %w", err)
	}

	content := string(data)

	// The published template references the disk relative to the yaml, but
	// limactl resolves relative locations against its own working directory —
	// rewrite to an absolute path.
	// imageDir is .../images/<version>/<arch>, so its base is the arch.
	absDisk, err := filepath.Abs(filepath.Join(imageDir, DiskFileName(filepath.Base(imageDir))))
	if err != nil {
		return "", fmt.Errorf("resolve disk path: %w", err)
	}
	content = regexp.MustCompile(`location: \S+\.qcow2`).ReplaceAllString(content, "location: "+absDisk)

	if opts.Port != 0 {
		content = regexp.MustCompile(`hostPort: \d+`).ReplaceAllString(content, fmt.Sprintf("hostPort: %d", opts.Port))
	}
	if opts.APIPort != 0 {
		// Forward the k3s API so host tooling (kubectl, devspace, helm) can
		// reach the cluster. Inserted as the first portForwards entry; 6443 on
		// the host is commonly taken by other local clusters.
		apiForward := fmt.Sprintf("portForwards:\n  - guestPort: 6443\n    hostIP: \"127.0.0.1\"\n    hostPort: %d", opts.APIPort)
		content = strings.Replace(content, "portForwards:", apiForward, 1)
	}
	if opts.CPUs != 0 {
		content = regexp.MustCompile(`(?m)^cpus: .*$`).ReplaceAllString(content, fmt.Sprintf("cpus: %d", opts.CPUs))
	}
	if opts.Memory != "" {
		content = regexp.MustCompile(`(?m)^memory: .*$`).ReplaceAllString(content, fmt.Sprintf("memory: %s", opts.Memory))
	}

	rendered := filepath.Join(imageDir, "lima.rendered.yaml")
	if err := os.WriteFile(rendered, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write rendered lima config: %w", err)
	}
	return rendered, nil
}
