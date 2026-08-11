package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/local"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/agynio/agyn-cli/internal/terminal"
	"github.com/spf13/cobra"
)

var (
	localDebug    bool
	localInstance string
)

func newLocalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "Manage local Agyn platform VMs",
		Long: "Download the prebuilt Agyn platform VM image and manage its lifecycle.\n" +
			"The platform runs in a Lima VM serving https://*." + local.BaseDomain + " on the\n" +
			"configured port.\n\n" +
			"One VM is the ordinary case and needs no naming. Run more than one — to move\n" +
			"data between versions an upgrade cannot bridge, or to keep separate clusters\n" +
			"side by side — with --instance, or choose one for good with 'agyn local select'.",
		// Every `agyn local` command acts on exactly one VM. Resolving it here,
		// before any command body runs, is what lets the rest of the code read
		// the choice instead of passing it down through every call.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if parent := cmd.Root().PersistentPreRunE; parent != nil {
				if err := parent(cmd, args); err != nil {
					return err
				}
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			local.Use(cfg.ResolveInstanceName(localInstance))
			return nil
		},
	}

	cmd.PersistentFlags().BoolVar(&localDebug, "debug", false, "Stream the raw output of limactl, helm and kubectl instead of the step display")
	cmd.PersistentFlags().StringVar(&localInstance, "instance", "",
		"VM to act on (default: the selected one, then \""+config.DefaultInstanceName+"\")")

	cmd.AddCommand(newLocalStartCmd())
	cmd.AddCommand(newLocalListCmd())
	cmd.AddCommand(newLocalSelectCmd())
	cmd.AddCommand(newLocalUseCmd())
	cmd.AddCommand(newLocalStopCmd())
	cmd.AddCommand(newLocalRestartCmd())
	cmd.AddCommand(newLocalStatusCmd())
	cmd.AddCommand(newLocalDeleteCmd())
	cmd.AddCommand(newLocalUpgradeCmd())
	cmd.AddCommand(newLocalLoadImageCmd())
	cmd.AddCommand(newLocalDoctorCmd())
	cmd.AddCommand(newLocalConfigCmd())
	cmd.AddCommand(newLocalCACmd())
	cmd.AddCommand(newLocalCredentialsCmd())
	cmd.AddCommand(newLocalKubeconfigCmd())
	cmd.AddCommand(newLocalResetCmd())

	return cmd
}

type localStartFlags struct {
	version       string
	port          int
	cpus          int
	memory        string
	installCA     bool
	noCA          bool
	installDeps   bool
	noInstallDeps bool
	downloadOnly  bool
	yes           bool
	profiles      localProfileFlags
}

func newLocalStartCmd() *cobra.Command {
	flags := localStartFlags{}

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Download the platform VM image if needed and boot it",
		Long: "Boots the platform VM and leaves the machine able to run every other `agyn`\n" +
			"command against it: the Gateway is given a bootstrap token generated for this\n" +
			"install, and the endpoint, organization and CA are recorded as a profile.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLocalStart(cmd, flags)
		},
	}

	cmd.Flags().StringVar(&flags.version, "version", "", "Image version (default: from config, then \"latest\")")
	cmd.Flags().IntVar(&flags.port, "port", 0, "Ingress host port (default: from config, then 2496)")
	cmd.Flags().IntVar(&flags.cpus, "cpus", 0, "VM CPUs (default: from config, then 4)")
	cmd.Flags().StringVar(&flags.memory, "memory", "", "VM memory (default: from config, then 8GiB)")
	cmd.Flags().BoolVar(&flags.installCA, "install-ca", false, "Install the local CA into the system trust store (needs sudo)")
	cmd.Flags().BoolVar(&flags.noCA, "no-ca", false, "Skip certificate handling entirely")
	cmd.Flags().BoolVar(&flags.installDeps, "install-deps", false, "Install missing host tools without asking")
	cmd.Flags().BoolVar(&flags.noInstallDeps, "no-install-deps", false, "Never install host tools; print what is missing and stop")
	cmd.Flags().BoolVar(&flags.downloadOnly, "download-only", false, "Download and verify the image without starting the VM")
	cmd.Flags().BoolVarP(&flags.yes, "yes", "y", false, "Non-interactive: accept defaults, never prompt")
	addLocalProfileFlags(cmd, &flags.profiles)

	return cmd
}

func runLocalStart(cmd *cobra.Command, flags localStartFlags) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	interactive := !flags.yes && isTerminal()
	steps := newSteps(cmd)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	instanceName := local.InstanceName()
	_, configured := cfg.Local.Instances[instanceName]
	firstRun := !configured

	if err := runPreflight(cmd, steps, flags, interactive, firstRun); err != nil {
		return err
	}

	// Resolve settings: flags > config > defaults, with a first-run wizard.
	settings := resolveInstancePorts(cfg, instanceName, cfg.InstanceSettings(instanceName))
	// The version *spec* that gets persisted: a --version flag is ephemeral
	// for this run only, so "latest" in the config stays "latest".
	configVersion := settings.Version
	if flags.version != "" {
		settings.Version = flags.version
	}
	if flags.port != 0 {
		settings.Port = flags.port
	}
	if flags.cpus != 0 {
		settings.CPUs = flags.cpus
	}
	if flags.memory != "" {
		settings.Memory = flags.memory
	}

	instance, err := local.GetInstance()
	if err != nil {
		return err
	}

	if instance.Exists {
		if instance.Status != "Running" {
			step := steps.Start("Starting the VM")
			limaIO, err := openRunLog(cmd)
			if err != nil {
				return step.Fail(err)
			}
			defer limaIO.close()
			if err := local.Start(limaIO.stdout, limaIO.stderr, vmOptions(settings)); err != nil {
				step.Fail(err)
				limaIO.reportFailure(cmd)
				return err
			}
			step.Done("")
		}
		return finishLocalStart(cmd, steps, flags, settings.Port)
	}

	// Asked only when the port this VM would take is not free. A first run was
	// asked unconditionally, which put a question with one sensible answer in
	// front of every install -- and in front of a second VM, whose port was
	// already chosen for being free.
	if err := checkPortAvailable(settings.Port); err != nil {
		if !interactive {
			return err
		}
		fmt.Fprintf(stderr, "%v\n", err)
		settings.Port = promptPort(cmd, settings.Port+1)
	}

	version, err := local.ResolveVersion(settings.Version)
	if err != nil {
		return err
	}
	arch, err := local.Arch()
	if err != nil {
		return err
	}

	imageDir, err := ensureImage(steps, version, arch)
	if err != nil {
		return err
	}

	persisted := settings
	persisted.Version = configVersion
	if err := config.SaveInstance(instanceName, persisted); err != nil {
		return err
	}

	if flags.downloadOnly {
		fmt.Fprintf(stdout, "Image ready in %s\n", imageDir)
		return nil
	}

	step := steps.Start("Creating the VM")
	step.Detail("first boot takes about a minute")
	limaIO, err := openRunLog(cmd)
	if err != nil {
		return step.Fail(err)
	}
	defer limaIO.close()
	if err := local.CreateAndStart(imageDir, vmOptions(settings), limaIO.stdout, limaIO.stderr); err != nil {
		step.Fail(err)
		limaIO.reportFailure(cmd)
		return err
	}
	step.Done("")

	if !flags.noCA {
		if err := handleCAAfterStart(cmd, steps, flags, interactive); err != nil {
			return err
		}
	}

	return finishLocalStart(cmd, steps, flags, settings.Port)
}

// ensureImage downloads and decompresses the image unless this machine already
// has it, reporting each phase as its own step: one is minutes of network with
// a byte count to show, the other minutes of CPU with nothing to show.
func ensureImage(steps *terminal.Steps, version, arch string) (string, error) {
	dir, present, err := local.HaveImage(version, arch)
	if err != nil {
		return "", err
	}
	if present {
		steps.Report("Image "+version, arch+", already downloaded")
		return dir, nil
	}

	step := steps.Start("Downloading image " + version)
	step.Detail(arch)
	if err := local.FetchImage(version, arch, step.Detail); err != nil {
		return "", step.Fail(err)
	}
	step.Done(arch)

	step = steps.Start("Decompressing the image")
	step.Detail("about 4.6 GB")
	dir, err = local.DecompressImage(version, arch)
	if err != nil {
		return "", step.Fail(err)
	}
	step.Done("")
	return dir, nil
}

// finishLocalStart waits for the platform, provisions the credentials that make
// the VM usable from the host, and closes with the one thing to do next.
func finishLocalStart(cmd *cobra.Command, steps *terminal.Steps, flags localStartFlags, port int) error {
	step := steps.Start("Waiting for the platform")
	readiness, err := local.WaitForReady(port, platformReadyTimeout, step.Detail)
	if err != nil {
		step.Fail(err)
		fmt.Fprintf(cmd.ErrOrStderr(),
			"\nThe VM is running. Watch it with `agyn local status`, and finish setup with:\n  agyn local credentials\n")
		return err
	}
	step.Done("")

	if !flags.profiles.noProfile {
		if err := provisionLocalProfile(cmd, steps, flags.profiles, port, !flags.noCA, readiness.Organization); err != nil {
			return err
		}
	}

	steps.CallToAction("Open the console", local.ConsoleURL(port))
	return nil
}

// platformReadyTimeout bounds the wait. A first boot has to start every
// workload in the cluster and then provision the platform's own resources; five
// minutes is past the point where more waiting is the wrong answer.
const platformReadyTimeout = 5 * time.Minute

func handleCAAfterStart(cmd *cobra.Command, steps *terminal.Steps, flags localStartFlags, interactive bool) error {
	step := steps.Start("Reading the VM's certificate authority")
	if _, err := local.EnsureCA(); err != nil {
		return step.Fail(err)
	}
	step.Done("")

	choice := caInstall
	if !flags.installCA && interactive {
		// Asked outside a step: sudo prompts for a password on this terminal,
		// and a spinner redrawing over the prompt makes it unreadable.
		choice = promptCAChoice(cmd)
	}

	switch choice {
	case caExport:
		path, err := exportCA()
		if err != nil {
			return steps.Failed("Exporting the CA", err)
		}
		steps.Report("Exporting the CA", path)
		steps.Note("trust it yourself, or later with: agyn local ca install")
		return nil
	case caSkip:
		steps.Skipped("Trusting the CA", "browsers will warn; install later with: agyn local ca install")
		return nil
	}

	if err := local.InstallCA(); err != nil {
		return steps.Failed("Trusting the CA",
			fmt.Errorf("%w — retry with `agyn local ca install`, or start with --no-ca", err))
	}
	steps.Report("Trusting the CA", "installed in the system trust store")
	return nil
}

// What to do with the CA the VM signs its certificates with.
type caChoice int

const (
	caInstall caChoice = iota
	caExport
	caSkip
)

// caChoiceItems is the picker's rows, in the order the constants above index
// them — the picker returns a position, so the two are one definition split in
// half, and a row inserted without its constant would install a CA on a machine
// whose owner chose to skip.
func caChoiceItems() []terminal.PickItem {
	items := make([]terminal.PickItem, 3)
	items[caInstall] = terminal.PickItem{
		Label: "Install it in the system trust store", Detail: "asks for sudo; browsers stop warning", Current: true}
	items[caExport] = terminal.PickItem{
		Label: "Export it to a file", Detail: "trust it yourself, wherever you need it"}
	items[caSkip] = terminal.PickItem{
		Label: "Skip", Detail: "browsers will warn on every platform URL"}
	return items
}

// promptCAChoice offers the three answers there are, rather than a yes/no whose
// no is a dead end. Someone who will not hand sudo to a CLI still needs the
// certificate — declining used to leave them with a browser warning and no
// file, which is the case the export exists for.
func promptCAChoice(cmd *cobra.Command) caChoice {
	choice, err := terminal.Pick(os.Stdin, cmd.OutOrStdout(),
		"The VM signs its certificates with its own CA. What should happen to it?", caChoiceItems())
	if err != nil {
		// Cancelled, or no terminal after all: the safe reading of "no answer"
		// is the one that changes nothing on this machine.
		return caSkip
	}
	return caChoice(choice)
}

// exportCA writes the certificate beside the user, in the directory they ran
// the command from, and returns where it went.
func exportCA() (string, error) {
	source, err := local.EnsureCA()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read CA: %w", err)
	}
	name := fmt.Sprintf("agyn-%s-ca.pem", local.InstanceName())
	if err := os.WriteFile(name, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return name, nil
	}
	return path, nil
}

// runPreflight is the dependency and environment check `agyn local doctor`
// reports, run before every start. A host tool that is missing or too old is
// the most common reason a first run fails, and left to limactl the failure
// arrives as a tool error rather than a sentence naming what to install.
func runPreflight(cmd *cobra.Command, steps *terminal.Steps, flags localStartFlags, interactive, firstRun bool) error {
	step := steps.Start("Checking prerequisites")
	checks := local.Preflight(local.PreflightOptions{Space: firstRun})
	failures := local.BlockingFailures(checks)
	if len(failures) == 0 {
		step.Done(preflightSummary(checks))
		return nil
	}
	step.Fail(fmt.Errorf("%s", failureSummary(failures)))

	tools := local.InstallableTools(checks)
	if len(tools) == 0 {
		return reportUnfixable(cmd, failures)
	}
	command, installable := local.InstallCommand(tools)
	if !installable {
		return reportUnfixable(cmd, failures)
	}

	install := flags.installDeps
	switch {
	case flags.noInstallDeps:
		install = false
	case install:
	case interactive:
		fmt.Fprintf(cmd.OutOrStdout(), "\n  Missing: %s\n  Install with: %s\n\n", toolList(tools), command)
		install = promptYesNo(cmd, "Run it now?", true)
	}
	if !install {
		return reportUnfixable(cmd, failures)
	}

	// Run with the terminal attached rather than under a step: brew reports its
	// own progress, and an install that needs sudo has to be able to ask.
	steps.Note("$ " + command)
	if err := local.InstallTools(tools, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
		return err
	}

	step = steps.Start("Re-checking prerequisites")
	checks = local.Preflight(local.PreflightOptions{Space: firstRun})
	if failures := local.BlockingFailures(checks); len(failures) > 0 {
		step.Fail(fmt.Errorf("%s", failureSummary(failures)))
		return reportUnfixable(cmd, failures)
	}
	step.Done(preflightSummary(checks))
	return nil
}

// reportUnfixable prints what is wrong and what would fix it, and stops. There
// is nothing further to try: every remaining failure needs a decision, a
// password, or a disk.
func reportUnfixable(cmd *cobra.Command, failures []local.Check) error {
	stderr := cmd.ErrOrStderr()
	fmt.Fprintln(stderr)
	for _, failure := range failures {
		fmt.Fprintf(stderr, "  %s: %s\n", failure.Name, failure.Detail)
		if failure.Fix != "" {
			fmt.Fprintf(stderr, "    fix: %s\n", failure.Fix)
		}
	}
	fmt.Fprintln(stderr)
	return fmt.Errorf("prerequisites are not met")
}

// preflightSummary names the host tools and their versions. The environment
// checks are deliberately not in it: "virtualization available" says nothing
// once it passed, and the free-space figure would push the line past the width
// of a terminal. `agyn local doctor` is where all of them are listed.
func preflightSummary(checks []local.Check) string {
	parts := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Tool() && check.State == local.CheckOK {
			parts = append(parts, check.Name+" "+check.Detail)
		}
	}
	return strings.Join(parts, ", ")
}

func failureSummary(failures []local.Check) string {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		parts = append(parts, failure.Name+" "+failure.Detail)
	}
	return strings.Join(parts, "; ")
}

func toolList(tools []local.Tool) string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Label)
	}
	return strings.Join(names, ", ")
}

// newSteps renders the progress of one command. --debug streams the tool output
// the steps exist to hide, so it turns the animation off rather than redrawing
// over it.
func newSteps(cmd *cobra.Command) *terminal.Steps {
	if localDebug {
		return terminal.NewPlainSteps(cmd.OutOrStdout())
	}
	return terminal.NewSteps(cmd.OutOrStdout())
}

func newLocalStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the platform VM (state is preserved)",
		RunE: func(cmd *cobra.Command, args []string) error {
			instance, err := local.GetInstance()
			if err != nil {
				return err
			}
			if !instance.Exists {
				return fmt.Errorf("no VM exists; run `agyn local start` first")
			}
			if instance.Status != "Running" {
				fmt.Fprintln(cmd.OutOrStdout(), "VM is not running.")
				return nil
			}
			limaIO, err := openRunLog(cmd)
			if err != nil {
				return err
			}
			defer limaIO.close()
			if err := local.Stop(limaIO.stdout, limaIO.stderr); err != nil {
				limaIO.reportFailure(cmd)
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "VM stopped.")
			return nil
		},
	}
}

func newLocalRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the platform VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			steps := newSteps(cmd)
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))
			instance, err := local.GetInstance()
			if err != nil {
				return err
			}
			if !instance.Exists {
				return fmt.Errorf("no VM exists; run `agyn local start` first")
			}
			limaIO, err := openRunLog(cmd)
			if err != nil {
				return err
			}
			defer limaIO.close()
			if instance.Status == "Running" {
				step := steps.Start("Stopping the VM")
				if err := local.Stop(limaIO.stdout, limaIO.stderr); err != nil {
					step.Fail(err)
					limaIO.reportFailure(cmd)
					return err
				}
				step.Done("")
			}
			step := steps.Start("Starting the VM")
			if err := local.Start(limaIO.stdout, limaIO.stderr, vmOptions(settings)); err != nil {
				step.Fail(err)
				limaIO.reportFailure(cmd)
				return err
			}
			step.Done("")
			// The same finishing work as a start, because a restart is how an
			// edited port takes effect: the forward is only half of it, and
			// without repointing the in-VM URLs and the profile the new port
			// would answer while sign-in bounced to the old one. Idempotent
			// when nothing changed.
			return finishLocalStart(cmd, steps, localStartFlags{}, settings.Port)
		},
	}
}

type localStatusOutput struct {
	Instance  local.Instance   `json:"instance" yaml:"instance"`
	Port      int              `json:"port" yaml:"port"`
	Name      string           `json:"name" yaml:"name"`
	Version   string           `json:"version" yaml:"version"`
	Endpoints []local.Endpoint `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
	CATrusted bool             `json:"ca_trusted" yaml:"ca_trusted"`
}

func newLocalStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show VM state, configuration, and endpoint health",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))

			instance, err := local.GetInstance()
			if err != nil {
				return err
			}

			status := localStatusOutput{
				Instance: instance,
				Name:     local.InstanceName(),
				Port:     settings.Port,
				Version:  settings.Version,
			}
			if instance.Status == "Running" {
				status.Endpoints = local.CheckEndpoints(local.Endpoints(settings.Port))
			}
			if info, err := local.InspectCA(); err == nil {
				status.CATrusted = info.Trusted
			}

			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, status)
			}

			state := "not created"
			if instance.Exists {
				state = instance.Status
			}
			rows := [][]string{
				{"State", state},
				{"Version", status.Version},
				{"Port", strconv.Itoa(status.Port)},
				{"CA trusted", strconv.FormatBool(status.CATrusted)},
			}
			for _, endpoint := range status.Endpoints {
				health := "unreachable"
				if endpoint.Healthy {
					health = "ok"
				}
				rows = append(rows, []string{endpoint.Name, endpoint.URL + " (" + health + ")"})
			}
			return output.Table{Headers: []string{"FIELD", "VALUE"}, Rows: rows}.Render(cmd.OutOrStdout())
		},
	}
}

func newLocalDeleteCmd() *cobra.Command {
	var purge bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove the platform VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			instance, err := local.GetInstance()
			if err != nil {
				return err
			}
			if !instance.Exists && !purge {
				fmt.Fprintln(cmd.OutOrStdout(), "No VM to delete.")
				return nil
			}

			if !yes && isTerminal() {
				prompt := "Delete the VM and all its state?"
				if purge {
					prompt = "Delete the VM and all downloaded images?"
				}
				if !promptYesNo(cmd, prompt, false) {
					return fmt.Errorf("aborted")
				}
			}

			if instance.Exists {
				limaIO, err := openRunLog(cmd)
				if err != nil {
					return err
				}
				defer limaIO.close()
				if err := local.Delete(limaIO.stdout, limaIO.stderr); err != nil {
					limaIO.reportFailure(cmd)
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "VM deleted.")
			}
			if purge {
				dir, err := local.Dir()
				if err != nil {
					return err
				}
				if err := os.RemoveAll(dir); err != nil {
					return fmt.Errorf("purge %s: %w", dir, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", dir)
				if err := forgetLocalProfile(cmd); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&purge, "purge", false, "Also remove downloaded images, certificates, and the local profile")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")

	return cmd
}

func newLocalUpgradeCmd() *cobra.Command {
	var resume bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the platform in the running VM to the latest charts",
		Long: "Upgrades the agyn-platform Helm release inside the VM to\n" +
			"the newest published charts, keeping the VM and everything in it.\n\n" +
			"This does not replace the disk image, so k3s, Istio, cert-manager and\n" +
			"OpenZiti stay as they were baked. To move those — or to get a clean\n" +
			"machine — recreate the VM with 'agyn local delete' and 'agyn local start'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Helm's and kubectl's own output goes to the log; what reaches the
			// terminal is which releases moved and where to, which is the whole
			// answer the user asked for.
			// No token to put back afterwards: the Gateway reads it from a
			// Secret the chart reuses rather than from the Deployment spec it
			// re-renders, so an upgrade leaves this install's token alone.
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))

			runLog, err := openRunLog(cmd)
			if err != nil {
				return err
			}
			defer runLog.close()

			steps := newSteps(cmd)
			changed, err := local.UpgradePlatform(steps, runLog.stdout, "", resume, settings.Port)
			if err != nil {
				runLog.reportFailure(cmd)
				return err
			}
			if !changed {
				return nil
			}

			if err := restoreIngressPort(cmd, steps, settings.Port, runLog); err != nil {
				return err
			}

			// The rollout is waited on here rather than by Helm, because it can
			// only succeed after the repair above: an upgrade reverts the
			// browser-facing URLs to the chart's port, and the workloads holding
			// them do not start until they are pointed back.
			step := steps.Start("Waiting for the platform")
			if _, err := local.WaitForReady(settings.Port, platformReadyTimeout, step.Detail); err != nil {
				return step.Fail(err)
			}
			step.Done("")
			return nil
		},
	}

	cmd.Flags().BoolVar(&resume, "resume", false,
		"Continue an upgrade that was interrupted, rather than being refused by the release it left in flight")

	return cmd
}

// restoreIngressPort points the browser-facing URLs back at the port this host
// forwards.
//
// Helm rewrites every Deployment the charts own, and those URLs are not in the
// charts: they were set with `kubectl set env` when the host chose a port. Left
// reverted, the Gateway fetches OIDC discovery from a port the ingress no
// longer publishes and every sign-in breaks.
func restoreIngressPort(cmd *cobra.Command, steps *terminal.Steps, port int, runLog *limaIO) error {
	step := steps.Start("Restoring the browser-facing port")
	if _, err := local.SetIngressPort(port, step.Detail); err != nil {
		step.Fail(err)
		runLog.reportFailure(cmd)
		return err
	}
	step.Done(strconv.Itoa(port))
	return nil
}

func newLocalLoadImageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "load-image IMAGE [IMAGE...]",
		Short: "Load host-built container images into the VM's cluster",
		Long: "Streams images from the host's docker daemon into the VM's k3s image\n" +
			"store, so a workload can run an image that was never pushed anywhere.\n\n" +
			"This is the k3s counterpart of `k3d image import`: the images become\n" +
			"available to the cluster under the same reference they have on the host.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return loadImages(cmd, args)
		},
	}
}

// loadImages pipes `docker save` straight into the guest's importer.
//
// Streamed rather than staged: these archives run to several gigabytes, and
// writing one to the host and again to the guest asks for room neither is
// guaranteed to have.
func loadImages(cmd *cobra.Command, images []string) error {
	instance, err := local.GetInstance()
	if err != nil {
		return err
	}
	if !instance.Exists || !strings.EqualFold(instance.Status, "Running") {
		return fmt.Errorf("the VM is not running; start it with: agyn local start")
	}

	save := exec.Command("docker", append([]string{"save"}, images...)...)
	var saveErr bytes.Buffer
	save.Stderr = &saveErr
	stdout, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("read the image archive: %w", err)
	}
	if err := save.Start(); err != nil {
		return fmt.Errorf("run docker save: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Loading %d image(s) into the VM...\n", len(images))
	out, importErr := local.ShellStdin(stdout, "sudo", "k3s", "ctr", "images", "import", "-")

	// Wait regardless: an importer that failed leaves docker save writing into
	// a closed pipe, and its own error is the more useful of the two.
	waitErr := save.Wait()
	if waitErr != nil {
		return fmt.Errorf("docker save: %w: %s", waitErr, strings.TrimSpace(saveErr.String()))
	}
	if importErr != nil {
		return fmt.Errorf("import images into the VM: %w", importErr)
	}
	if trimmed := strings.TrimSpace(out); trimmed != "" {
		fmt.Fprintln(cmd.OutOrStdout(), trimmed)
	}
	for _, image := range images {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", image)
	}
	return nil
}

func newLocalDoctorCmd() *cobra.Command {
	var fix bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check host tools and environment for the local VM",
		Long: "Runs the same preflight `agyn local start` runs: the host tools and their\n" +
			"versions, room on disk, the ports, and whether this machine can run a VM at\n" +
			"all. --fix installs the tools that are missing.",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}

			checks := local.Preflight(doctorOptions())
			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, checks)
			}

			if err := printChecks(cmd.OutOrStdout(), checks); err != nil {
				return err
			}
			failures := local.BlockingFailures(checks)
			if len(failures) == 0 {
				return nil
			}

			tools := local.InstallableTools(checks)
			command, installable := local.InstallCommand(tools)
			if !fix || !installable {
				if installable {
					fmt.Fprintf(cmd.OutOrStdout(), "\nInstall the missing tools with `agyn local doctor --fix`, or run:\n  %s\n", command)
				}
				return fmt.Errorf("%d check(s) failed", len(failures))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\n$ %s\n", command)
			if err := local.InstallTools(tools, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout())
			checks = local.Preflight(doctorOptions())
			if err := printChecks(cmd.OutOrStdout(), checks); err != nil {
				return err
			}
			if remaining := local.BlockingFailures(checks); len(remaining) > 0 {
				return fmt.Errorf("%d check(s) still failing", len(remaining))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "Install the host tools that are missing")

	return cmd
}

// doctorOptions checks everything a start would, minus what would report a
// false failure: a running VM holds its own ports, and finding them taken by
// itself is not a problem to report.
func doctorOptions() local.PreflightOptions {
	opts := local.PreflightOptions{Space: true}
	cfg, err := config.Load()
	if err != nil {
		return opts
	}
	settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))
	// GetInstance needs limactl, which is one of the things being checked; an
	// error here means the tool check is about to report the real problem.
	if instance, err := local.GetInstance(); err == nil && !instance.Exists {
		opts.Ports = []int{settings.Port, settings.APIPort}
	}
	return opts
}

func printChecks(out io.Writer, checks []local.Check) error {
	rows := make([][]string, 0, len(checks))
	for _, check := range checks {
		detail := check.Detail
		if check.Fix != "" {
			detail += " — fix: " + check.Fix
		}
		rows = append(rows, []string{check.Name, string(check.State), detail})
	}
	return output.Table{Headers: []string{"CHECK", "STATUS", "DETAIL"}, Rows: rows}.Render(out)
}

// limaIO routes the output of the tools a command drives — limactl, and the
// helm and kubectl it runs inside the VM. To the terminal with --debug,
// otherwise to ~/.agyn/local/logs/<vm>.log with an automatic tail on failure.
type limaIO struct {
	stdout io.Writer
	stderr io.Writer
	file   *os.File
	path   string
}

func openRunLog(cmd *cobra.Command) (*limaIO, error) {
	if localDebug {
		return &limaIO{stdout: cmd.OutOrStdout(), stderr: cmd.ErrOrStderr()}, nil
	}

	dir, err := local.Dir()
	if err != nil {
		return nil, err
	}
	// One log per VM. A single shared file is truncated by whichever command
	// opens it next, so two commands running at once write into each other's:
	// an upgrade's failure printed the tail of a delete running beside it,
	// which reads as the upgrade having shut the VM down.
	dir = filepath.Join(dir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, local.InstanceName()+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open run log: %w", err)
	}
	return &limaIO{stdout: file, stderr: file, file: file, path: path}, nil
}

func (l *limaIO) close() {
	if l.file != nil {
		l.file.Close()
	}
}

// reportFailure surfaces the tail of the captured log when a lima operation
// fails without --debug.
func (l *limaIO) reportFailure(cmd *cobra.Command) {
	if l.file == nil {
		return
	}
	l.file.Sync()
	data, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "\nTool output (last lines; full log: %s):\n%s\n",
		l.path, strings.Join(lines, "\n"))
	reportBootLogs(cmd)
}

// reportBootLogs prints the tail of the logs a failed boot leaves behind.
//
// A VM that dies early reports only "exiting, status={Running:false …
// Errors:[]}" and points at ha.stderr.log for the reason. That file lives in
// the instance directory, which the failure path deletes, so local.CreateAndStart
// copies these up beside the run log first -- read them from there.
func reportBootLogs(cmd *cobra.Command) {
	dir, err := local.Dir()
	if err != nil {
		return
	}
	for _, name := range local.BootLogNames {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		lines := strings.Split(text, "\n")
		if len(lines) > 30 {
			lines = lines[len(lines)-30:]
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "\n%s (last lines; full log: %s):\n%s\n",
			name, path, strings.Join(lines, "\n"))
	}
}

// resolveInstancePorts fills in ports a VM has not been given.
//
// The default VM keeps the well-known 2496/6445, which is what every doc and
// every URL assumes. A second VM cannot have those, and making the user work
// out which pair is free turns "run another cluster" into arithmetic — so its
// ports are found here, skipping anything already listening or already claimed
// by another configured VM.
// vmOptions is the VM shape the user has configured. Creation and start both
// need it: start applies it too, so a size changed after creation takes effect.
func vmOptions(settings config.LocalInstance) local.VMOptions {
	return local.VMOptions{
		Port:    settings.Port,
		APIPort: settings.APIPort,
		CPUs:    settings.CPUs,
		Memory:  settings.Memory,
	}
}

func resolveInstancePorts(cfg *config.Config, name string, settings config.LocalInstance) config.LocalInstance {
	if name == config.DefaultInstanceName {
		if settings.Port == 0 {
			settings.Port = config.DefaultLocalPort
		}
		if settings.APIPort == 0 {
			settings.APIPort = config.DefaultLocalAPIPort
		}
		return settings
	}

	taken := map[int]bool{}
	for other, otherSettings := range cfg.Local.Instances {
		if other == name {
			continue
		}
		taken[otherSettings.Port] = true
		taken[otherSettings.APIPort] = true
	}
	if settings.Port == 0 {
		settings.Port = nextFreePort(config.DefaultLocalPort+1, taken)
	}
	if settings.APIPort == 0 {
		settings.APIPort = nextFreePort(config.DefaultLocalAPIPort+1, taken)
	}
	return settings
}

func nextFreePort(start int, taken map[int]bool) int {
	for port := start; port < start+200; port++ {
		if taken[port] {
			continue
		}
		if checkPortAvailable(port) == nil {
			taken[port] = true
			return port
		}
	}
	return start
}

func checkPortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("port %d is already in use; choose another with --port or `agyn local config set port <n>`", port)
	}
	listener.Close()
	return nil
}

// isTerminal reports whether there is a human at stdin to prompt.
//
// A character-device check is not that check: /dev/null is a character device,
// so a command run from cron or a CI job with stdin redirected read as
// interactive, prompted, took EOF for the default answer and proceeded — which
// is exactly the "no prompts without a TTY" rule it was there to enforce.
func isTerminal() bool {
	return terminal.IsTerminal(os.Stdin)
}

func promptYesNo(cmd *cobra.Command, question string, defaultYes bool) bool {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s: ", question, suffix)

	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil {
		return defaultYes
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return defaultYes
	}
}

// promptPort asks for the one host port the VM publishes. Named for what it
// serves rather than for the component that terminates it: "ingress port" is
// the answer to a question nobody outside the cluster asked.
func promptPort(cmd *cobra.Command, suggested int) int {
	fmt.Fprintf(cmd.OutOrStdout(), "The platform is served at https://console.%s:PORT\n", local.BaseDomain)
	for {
		fmt.Fprintf(cmd.OutOrStdout(), "Port to serve it on [%d]: ", suggested)
		reader := bufio.NewReader(cmd.InOrStdin())
		line, err := reader.ReadString('\n')
		if err != nil {
			return suggested
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return suggested
		}
		port, err := strconv.Atoi(line)
		if err != nil || port < 1 || port > 65535 {
			fmt.Fprintln(cmd.ErrOrStderr(), "enter a port between 1 and 65535")
			continue
		}
		return port
	}
}

func init() {
	rootCmd.AddCommand(newLocalCmd())
}
