package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/local"
	"github.com/agynio/agyn-cli/internal/output"
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

	cmd.PersistentFlags().BoolVar(&localDebug, "debug", false, "Show raw VM manager output")
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
	cmd.AddCommand(newLocalDoctorCmd())
	cmd.AddCommand(newLocalConfigCmd())
	cmd.AddCommand(newLocalCACmd())
	cmd.AddCommand(newLocalCredentialsCmd())
	cmd.AddCommand(newLocalKubeconfigCmd())
	cmd.AddCommand(newLocalResetCmd())

	return cmd
}

type localStartFlags struct {
	version      string
	port         int
	cpus         int
	memory       string
	installCA    bool
	noCA         bool
	downloadOnly bool
	yes          bool
	profiles     localProfileFlags
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
	cmd.Flags().BoolVar(&flags.downloadOnly, "download-only", false, "Download and verify the image without starting the VM")
	cmd.Flags().BoolVarP(&flags.yes, "yes", "y", false, "Non-interactive: accept defaults, never prompt")
	addLocalProfileFlags(cmd, &flags.profiles)

	return cmd
}

func runLocalStart(cmd *cobra.Command, flags localStartFlags) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	interactive := !flags.yes && isTerminal()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	instanceName := local.InstanceName()
	_, configured := cfg.Local.Instances[instanceName]
	firstRun := !configured

	deps := local.CheckDependencies()
	if missing := local.MissingRequired(deps); len(missing) > 0 {
		for _, dep := range missing {
			fmt.Fprintf(stderr, "missing dependency: %s (install with: %s)\n", dep.Name, dep.Fix)
		}
		return fmt.Errorf("install the missing dependencies and retry")
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
		if instance.Status == "Running" {
			fmt.Fprintln(stdout, "VM is already running.")
			finishLocalStart(cmd, flags, settings.Port)
			return nil
		}
		fmt.Fprintln(stdout, "Starting VM (~30s)...")
		limaIO, err := openLimaIO(cmd)
		if err != nil {
			return err
		}
		defer limaIO.close()
		if err := local.Start(limaIO.stdout, limaIO.stderr, vmOptions(settings)); err != nil {
			limaIO.reportFailure(cmd)
			return err
		}
		fmt.Fprintln(stdout, "VM started.")
		finishLocalStart(cmd, flags, settings.Port)
		return nil
	}

	if interactive && firstRun && flags.port == 0 {
		settings.Port = promptPort(cmd, settings.Port)
	}
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

	fmt.Fprintf(stdout, "Using image version %s (%s)\n", version, arch)
	imageDir, err := local.EnsureImage(version, arch, stderr)
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

	fmt.Fprintln(stdout, "Creating and starting VM (first boot takes ~1m)...")
	limaIO, err := openLimaIO(cmd)
	if err != nil {
		return err
	}
	defer limaIO.close()
	if err := local.CreateAndStart(imageDir, vmOptions(settings), limaIO.stdout, limaIO.stderr); err != nil {
		limaIO.reportFailure(cmd)
		return err
	}
	fmt.Fprintln(stdout, "VM started.")

	if !flags.noCA {
		if err := handleCAAfterStart(cmd, flags, interactive); err != nil {
			fmt.Fprintf(stderr, "certificate setup incomplete: %v\n", err)
			fmt.Fprintln(stderr, "you can finish it later with: agyn local ca install")
		}
	}

	finishLocalStart(cmd, flags, settings.Port)
	return nil
}

// finishLocalStart waits for the platform, provisions the credentials that make
// the VM usable from the host, and prints the endpoint list.
//
// Provisioning failures are reported rather than returned: the VM is up and the
// endpoints are worth printing, and `agyn local credentials` finishes the job.
func finishLocalStart(cmd *cobra.Command, flags localStartFlags, port int) {
	ready := waitForPlatform(cmd, port)
	if !flags.profiles.noProfile {
		if !ready {
			fmt.Fprintln(cmd.ErrOrStderr(), "skipping credential setup while the platform is still starting")
			fmt.Fprintln(cmd.ErrOrStderr(), "run it once the platform is up with: agyn local credentials")
		} else if err := provisionLocalProfile(cmd, flags.profiles, port, !flags.noCA); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "credential setup incomplete: %v\n", err)
			fmt.Fprintln(cmd.ErrOrStderr(), "you can finish it later with: agyn local credentials")
		}
	}
	printLocalEndpoints(cmd.OutOrStdout(), port)
}

// waitAndPrintEndpoints waits for the platform, then prints the endpoint list.
func waitAndPrintEndpoints(cmd *cobra.Command, port int) {
	waitForPlatform(cmd, port)
	printLocalEndpoints(cmd.OutOrStdout(), port)
}

// waitForPlatform blocks until the in-VM ingress actually serves the platform:
// the guest reports "started" well before Istio and the apps are ready.
func waitForPlatform(cmd *cobra.Command, port int) bool {
	fmt.Fprint(cmd.OutOrStdout(), "Waiting for the platform to become ready...")
	if local.WaitForPlatform(port, 3*time.Minute) {
		fmt.Fprintln(cmd.OutOrStdout(), " ready.")
		return true
	}
	fmt.Fprintln(cmd.OutOrStdout(), " not ready yet; check `agyn local status` in a minute.")
	return false
}

func handleCAAfterStart(cmd *cobra.Command, flags localStartFlags, interactive bool) error {
	if _, err := local.EnsureCA(); err != nil {
		return err
	}

	install := flags.installCA
	if !install && interactive {
		install = promptYesNo(cmd, "Install the Agyn local CA into your system trust store? (asks for sudo)", true)
	}
	if !install {
		if !flags.installCA {
			fmt.Fprintln(cmd.OutOrStdout(), "Skipping CA install; browsers will warn about the certificate.")
			fmt.Fprintln(cmd.OutOrStdout(), "Install later with: agyn local ca install")
		}
		return nil
	}
	if err := local.InstallCA(); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "CA installed and trusted.")
	return nil
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
			limaIO, err := openLimaIO(cmd)
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
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))
			_ = settings
			instance, err := local.GetInstance()
			if err != nil {
				return err
			}
			if !instance.Exists {
				return fmt.Errorf("no VM exists; run `agyn local start` first")
			}
			limaIO, err := openLimaIO(cmd)
			if err != nil {
				return err
			}
			defer limaIO.close()
			if instance.Status == "Running" {
				fmt.Fprintln(cmd.OutOrStdout(), "Stopping VM...")
				if err := local.Stop(limaIO.stdout, limaIO.stderr); err != nil {
					limaIO.reportFailure(cmd)
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Starting VM (~30s)...")
			if err := local.Start(limaIO.stdout, limaIO.stderr, vmOptions(settings)); err != nil {
				limaIO.reportFailure(cmd)
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "VM started.")
			waitAndPrintEndpoints(cmd, settings.Port)
			return nil
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
				limaIO, err := openLimaIO(cmd)
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
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the platform in the running VM to the latest charts",
		Long: "Upgrades the agyn-platform and agyn-apps Helm releases inside the VM to\n" +
			"the newest published charts, keeping the VM and everything in it.\n\n" +
			"This does not replace the disk image, so k3s, Istio, cert-manager and\n" +
			"OpenZiti stay as they were baked. To move those — or to get a clean\n" +
			"machine — recreate the VM with 'agyn local delete' and 'agyn local start'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Streamed to the terminal, not the lima log: an upgrade that waits
			// on rollouts takes minutes, and the chart versions it reports are
			// the whole answer the user asked for.
			return local.UpgradePlatform(cmd.OutOrStdout(), cmd.ErrOrStderr(), "", "")
		},
	}
}

func newLocalDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check dependencies and environment for the local VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}

			deps := local.CheckDependencies()
			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, deps)
			}

			rows := make([][]string, 0, len(deps))
			ok := true
			for _, dep := range deps {
				state := "ok"
				detail := dep.Version
				if !dep.Found {
					state = "missing"
					detail = "install with: " + dep.Fix
					ok = false
				}
				rows = append(rows, []string{dep.Name, state, detail})
			}
			if err := (output.Table{Headers: []string{"DEPENDENCY", "STATUS", "DETAIL"}, Rows: rows}).Render(cmd.OutOrStdout()); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("some dependencies are missing")
			}
			return nil
		},
	}
}

// limaIO routes limactl output: to the terminal with --debug, otherwise to
// ~/.agyn/local/lima.log with an automatic tail on failure.
type limaIO struct {
	stdout io.Writer
	stderr io.Writer
	file   *os.File
	path   string
}

func openLimaIO(cmd *cobra.Command) (*limaIO, error) {
	if localDebug {
		return &limaIO{stdout: cmd.OutOrStdout(), stderr: cmd.ErrOrStderr()}, nil
	}

	dir, err := local.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, "lima.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lima log: %w", err)
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
	fmt.Fprintf(cmd.ErrOrStderr(), "\nVM manager output (last lines; full log: %s):\n%s\n",
		l.path, strings.Join(lines, "\n"))
	reportHostAgentFailure(cmd)
}

// reportHostAgentFailure prints the tail of the host agent's stderr.
//
// A boot that dies early reports only "exiting, status={Running:false ...
// Errors:[]}" and points at ha.stderr.log for the reason -- which is no use on
// a CI runner that is destroyed with the job. Whatever actually refused to
// start, qemu included, says so there and nowhere else.
func reportHostAgentFailure(cmd *cobra.Command) {
	limaHome, err := local.LimaHome()
	if err != nil {
		return
	}
	path := filepath.Join(limaHome, local.InstanceName(), "ha.stderr.log")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return
	}
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "\nHost agent stderr (last lines; full log: %s):\n%s\n",
		path, strings.Join(lines, "\n"))
}

func printLocalEndpoints(w interface{ Write([]byte) (int, error) }, port int) {
	fmt.Fprintln(w, "\nPlatform endpoints:")
	for _, endpoint := range local.Endpoints(port) {
		fmt.Fprintf(w, "  %-8s %s\n", endpoint.Name+":", endpoint.URL)
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

func isTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
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

func promptPort(cmd *cobra.Command, suggested int) int {
	for {
		fmt.Fprintf(cmd.OutOrStdout(), "Ingress port [%d]: ", suggested)
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
