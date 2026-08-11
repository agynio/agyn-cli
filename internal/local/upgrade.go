package local

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agynio/agyn-cli/internal/terminal"
)

// A chart cannot upgrade itself, so this one script stays outside it. It ships
// here rather than baked into the image because the image is what an upgrade
// exists to avoid replacing: a fix to the upgrade path itself would otherwise
// need a rebake to reach a machine that is already running.
//
//go:embed scripts/upgrade-platform.sh
var upgradeScript string

// marker prefixes the lines the script writes for the CLI to render. Everything
// else it emits is tool output, and belongs in the log.
const marker = "AGYN|"

// UpgradePlatform moves the platform releases in the running VM to newer chart
// versions, leaving the VM and its data alone.
//
// Upgrading by replacing the disk image would discard every database, thread
// and workload in the VM. That is the right way to get a clean machine — delete
// then start — but the wrong thing for "upgrade" to mean, because it is not
// reversible and destroys work the user did not ask to lose.
//
// The script reports its progress as steps and sends everything Helm and
// kubectl say to log: a Helm upgrade that waits on rollouts takes minutes, and
// what the user asked was which versions moved, not what the admission webhook
// thinks of the release's AuthorizationPolicies.
// Returns whether any release moved. The caller acts on that: an upgrade
// rewrites every Deployment the charts own, including the browser-facing URLs
// that were pointed at this host's port outside the release.
// resume clears the revision an interrupted upgrade left in flight before
// upgrading, which is what stops Helm refusing it.
func UpgradePlatform(steps *terminal.Steps, log io.Writer, platformVersion string, resume bool, ingressPort int) (bool, error) {
	args := []string{}
	// The host's port, so the upgrade renders the URLs this VM is actually
	// reachable on instead of the ones the image was baked with.
	if ingressPort > 0 {
		args = append(args, "--ingress-port", strconv.Itoa(ingressPort))
	}
	if resume {
		args = append(args, "--resume")
	}
	if platformVersion != "" {
		args = append(args, "--platform-version", platformVersion)
	}
	return runUpgradeScript(steps, log, args)
}

func runUpgradeScript(steps *terminal.Steps, log io.Writer, scriptArgs []string) (bool, error) {
	instance, err := GetInstance()
	if err != nil {
		return false, err
	}
	if !instance.Exists {
		return false, fmt.Errorf("no local VM; create one with 'agyn local start'")
	}
	if instance.Status != "Running" {
		return false, fmt.Errorf("the local VM is not running (%s); start it with 'agyn local start'", instance.Status)
	}

	// Piped to a shell rather than written into the VM: nothing to install, and
	// no baked copy left behind to drift from this caller.
	args := append([]string{"shell", InstanceName(), "--", "sudo", "bash", "-s", "--"}, scriptArgs...)

	reader, writer := io.Pipe()
	render := &upgradeRenderer{steps: steps, log: log}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		render.consume(reader)
	}()

	runErr := limactlStdin(strings.NewReader(upgradeScript), writer, log, args...)
	writer.Close()
	wg.Wait()

	if runErr != nil {
		// The script's own account of what went wrong, when it gave one: an
		// exit status says nothing a user can act on.
		if render.failure != "" {
			return false, fmt.Errorf("%s", render.failure)
		}
		return false, render.fail(fmt.Errorf("upgrade the platform in the VM: %w", runErr))
	}
	render.finish()
	return render.changed, nil
}

// upgradeRenderer turns the script's markers into steps, and everything else
// into log lines.
type upgradeRenderer struct {
	steps *terminal.Steps
	log   io.Writer

	mu      sync.Mutex
	step    *terminal.Step
	detail  string
	stopped chan struct{}
	changed bool
	failure string
}

func (r *upgradeRenderer) consume(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, marker) {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintln(r.log, line)
			}
			continue
		}
		fields := strings.SplitN(strings.TrimPrefix(line, marker), "|", 3)
		switch fields[0] {
		case "step":
			r.start(field(fields, 1), field(fields, 2))
		case "done":
			r.done(field(fields, 1))
		case "skip":
			r.skip(field(fields, 1))
		case "note":
			r.steps.Note(field(fields, 1))
		case "changed":
			r.changed = true
		case "fail":
			r.failure = field(fields, 1)
			r.fail(fmt.Errorf("%s", r.failure))
		}
	}
}

func field(fields []string, index int) string {
	if index < len(fields) {
		return fields[index]
	}
	return ""
}

// start opens a step and begins reporting the rollouts it is waiting on. A Helm
// upgrade with --wait is silent for as long as it takes, and the cluster is the
// only place that knows how far along it is.
func (r *upgradeRenderer) start(title, detail string) {
	r.close()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.step = r.steps.Start(title)
	r.detail = detail
	if detail != "" {
		r.step.Detail(detail)
	}
	stopped := make(chan struct{})
	r.stopped = stopped
	go r.pollRollouts(r.step, detail, stopped)
}

func (r *upgradeRenderer) pollRollouts(step *terminal.Step, base string, stopped chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stopped:
			return
		case <-ticker.C:
			rolling := RollingWorkloads(2)
			if len(rolling) == 0 {
				continue
			}
			detail := "waiting on " + strings.Join(rolling, ", ")
			if base != "" {
				detail = base + "  " + detail
			}
			step.Detail(detail)
		}
	}
}

func (r *upgradeRenderer) done(detail string) {
	r.stopPolling()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.step == nil {
		return
	}
	if detail == "" {
		detail = r.detail
	}
	r.step.Done(detail)
	r.step = nil
}

func (r *upgradeRenderer) skip(detail string) {
	r.stopPolling()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.step == nil {
		return
	}
	r.step.Skip(detail)
	r.step = nil
}

// close ends a step the script left open, which happens when the next step
// begins without one. The work did complete: the script emits a step per unit
// of work, and a failure ends the run rather than moving on.
func (r *upgradeRenderer) close() { r.done("") }

// fail marks whatever was in progress as the thing that failed, so the error is
// attributed to a step rather than printed after a line that still reads as
// running.
func (r *upgradeRenderer) fail(err error) error {
	r.stopPolling()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.step != nil {
		r.step.Fail(err)
		r.step = nil
		return err
	}
	return err
}

func (r *upgradeRenderer) finish() { r.close() }

func (r *upgradeRenderer) stopPolling() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped != nil {
		close(r.stopped)
		r.stopped = nil
	}
}
