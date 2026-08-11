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

// limactlStdin is limactl with something on the guest command's stdin, which is
// how a script the CLI carries gets run without being written into the VM.
func limactlStdin(stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	env, err := limaEnv()
	if err != nil {
		return err
	}
	cmd := exec.Command("limactl", args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
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
		// The delete below takes the instance directory with it, and that is
		// where the only account of what went wrong lives -- lima's own error
		// says "Errors:[]" and refers the reader to a file it is about to
		// destroy. Copy those out first.
		preserveBootLogs()
		// A failed creation leaves a half-registered instance behind that
		// would shadow the next attempt; remove it so retries start clean.
		_ = limactl(io.Discard, io.Discard, "delete", "--force", InstanceName())
		return err
	}
	return nil
}

// BootLogNames are the instance logs preserved when a boot fails, in the order
// they are worth reading: why the VM would not start, then how far the guest
// got if it did.
var BootLogNames = []string{"ha.stderr.log", "serial.log"}

// preserveBootLogs copies the instance's boot logs up into the local directory,
// beside the run log, so they outlive the instance. Best effort throughout: this
// runs on a path that has already failed and must not fail again.
func preserveBootLogs() {
	limaHome, err := LimaHome()
	if err != nil {
		return
	}
	dir, err := Dir()
	if err != nil {
		return
	}
	for _, name := range BootLogNames {
		data, err := os.ReadFile(filepath.Join(limaHome, InstanceName(), name))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
}

// Start boots an existing instance, first applying any size the user has
// changed since it was created. Lima reads the port forwards, cpus and memory
// from the instance's own config, which only creation writes, so without this
// an edited setting is stored and silently never takes effect.
func Start(stdout, stderr io.Writer, opts VMOptions) error {
	if err := applyInstanceSettings(opts); err != nil {
		return err
	}
	return limactl(stdout, stderr, "start", InstanceName())
}

func applyInstanceSettings(opts VMOptions) error {
	if opts.CPUs == 0 && opts.Memory == "" && opts.Port == 0 && opts.APIPort == 0 {
		return nil
	}
	limaHome, err := LimaHome()
	if err != nil {
		return err
	}
	path := filepath.Join(limaHome, InstanceName(), "lima.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read instance config: %w", err)
	}
	content := string(data)
	if opts.CPUs != 0 {
		content = regexp.MustCompile(`(?m)^cpus: .*$`).ReplaceAllString(content, fmt.Sprintf("cpus: %d", opts.CPUs))
	}
	if opts.Memory != "" {
		content = regexp.MustCompile(`(?m)^memory: .*$`).ReplaceAllString(content, fmt.Sprintf("memory: %s", opts.Memory))
	}
	// The ingress and API forwards are matched by guest port, because the host
	// port is the thing being changed and the two entries are otherwise
	// identical in shape.
	if opts.Port != 0 {
		content = replaceHostPort(content, ingressNodePort, opts.Port)
	}
	if opts.APIPort != 0 {
		content = replaceHostPort(content, kubeAPIPort, opts.APIPort)
	}
	if content == string(data) {
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write instance config: %w", err)
	}
	return nil
}

// Stop shuts the instance down.
func Stop(stdout, stderr io.Writer) error {
	return limactl(stdout, stderr, "stop", InstanceName())
}

// Delete removes the instance and its disk state.
func Delete(stdout, stderr io.Writer) error {
	return limactl(stdout, stderr, "delete", "--force", InstanceName())
}

// RunScript pipes a script the CLI carries into the guest and runs it there,
// returning its combined output.
//
// Nothing is installed in the VM and no copy is left behind to drift from this
// caller. That is the whole reason these scripts live with the CLI: one that
// lives in the image can only be corrected by building a new image and
// recreating the VM, which is the operation `upgrade` exists to avoid.
func RunScript(script string, args ...string) (string, error) {
	return RunScriptProgress(script, nil, args...)
}

// RunScriptProgress runs a script that reports where it has got to, calling
// onDetail for each AGYN|detail| line it writes. Everything else is the script's
// own log and is returned.
//
// Scripts that restart the cluster take minutes, and a step with a fixed label
// for that long cannot be told from a hang.
func RunScriptProgress(script string, onDetail func(string), args ...string) (string, error) {
	var out bytes.Buffer
	stdout := io.Writer(&out)
	if onDetail != nil {
		stdout = &detailScanner{onDetail: onDetail, rest: &out}
	}
	shellArgs := append([]string{"shell", InstanceName(), "--", "sudo", "bash", "-s", "--"}, args...)
	if err := limactlStdin(strings.NewReader(script), stdout, &out, shellArgs...); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// detailScanner splits a script's stdout into progress markers and everything
// else, line by line as it arrives.
type detailScanner struct {
	onDetail func(string)
	rest     io.Writer
	partial  []byte
}

func (d *detailScanner) Write(data []byte) (int, error) {
	d.partial = append(d.partial, data...)
	for {
		index := bytes.IndexByte(d.partial, '\n')
		if index < 0 {
			return len(data), nil
		}
		line := string(d.partial[:index])
		d.partial = d.partial[index+1:]
		if detail, ok := strings.CutPrefix(line, detailMarker); ok {
			d.onDetail(detail)
			continue
		}
		fmt.Fprintln(d.rest, line)
	}
}

// detailMarker prefixes a line a script writes for the CLI to show.
const detailMarker = "AGYN|detail|"

// RunScriptWithSecret runs a script whose input is a credential.
//
// The script is staged into the guest first, because stdin is the one channel
// that carries a secret without putting it in an argument list -- and the
// script itself would otherwise be occupying it. An argument list is readable
// by any user of the guest, and by anyone running ps on the host while the CLI
// works. The staged copy is mode 0700 under /run, which is tmpfs, and is
// removed whether or not the run succeeds.
func RunScriptWithSecret(script string, secret io.Reader, args ...string) (string, error) {
	path := "/run/agyn-" + InstanceName() + ".sh"

	var staged bytes.Buffer
	stage := []string{"shell", InstanceName(), "--", "sudo", "sh", "-c", "umask 077 && cat > " + path}
	if err := limactlStdin(strings.NewReader(script), &staged, &staged, stage...); err != nil {
		return "", fmt.Errorf("stage the script in the VM: %w: %s", err, strings.TrimSpace(staged.String()))
	}
	defer func() {
		_ = limactl(io.Discard, io.Discard, "shell", InstanceName(), "--", "sudo", "rm", "-f", path)
	}()

	var out bytes.Buffer
	runArgs := append([]string{"shell", InstanceName(), "--", "sudo", "bash", path}, args...)
	if err := limactlStdin(secret, &out, &out, runArgs...); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
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

// ShellStdin runs a command inside the guest with stdin piped from the host.
//
// Used to stream an image archive straight into the guest's container store
// rather than staging a multi-gigabyte file on a disk that may not have room
// for it.
func ShellStdin(stdin io.Reader, args ...string) (string, error) {
	env, err := limaEnv()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("limactl", append([]string{"shell", InstanceName(), "--"}, args...)...)
	cmd.Env = env
	cmd.Stdin = stdin
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
const (
	ingressNodePort = 32443
	kubeAPIPort     = 6443
)

// replaceHostPort rewrites the hostPort of the forward whose guestPort matches,
// leaving every other forward alone.
func replaceHostPort(content string, guestPort, hostPort int) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`(?m)(guestPort: %d\n(?:[ \t]+\S.*\n)*?[ \t]+hostPort: )\d+`, guestPort))
	return pattern.ReplaceAllString(content, fmt.Sprintf("${1}%d", hostPort))
}

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
