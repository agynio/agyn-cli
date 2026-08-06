package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/agynio/agyn-cli/internal/sync/loginservice"
	"github.com/agynio/agyn-cli/internal/sync/session"
	"github.com/spf13/cobra"
)

func newSandboxSyncDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage resume-at-login for sync sessions",
	}
	cmd.AddCommand(newSandboxSyncDaemonInstallCmd())
	cmd.AddCommand(newSandboxSyncDaemonUninstallCmd())
	cmd.AddCommand(newSandboxSyncDaemonStatusCmd())
	return cmd
}

func newSandboxSyncDaemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register a user-level service so sessions resume at login",
		Long: "Opt-in. Without it nothing resumes after a reboot: sessions persist and are\n" +
			"reported as not running until started again.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			path, err := loginservice.Install(executable)
			if err != nil {
				return describeLoginServiceError(err)
			}
			fmt.Fprintf(os.Stderr, "installed %s\nsessions will resume at your next login\n", path)
			return nil
		},
	}
}

func newSandboxSyncDaemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the resume-at-login service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := loginservice.Uninstall()
			if err != nil {
				return describeLoginServiceError(err)
			}
			fmt.Fprintf(os.Stderr, "removed %s\nsessions no longer resume at login; they are unaffected otherwise\n", path)
			return nil
		},
	}
}

func newSandboxSyncDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether resume-at-login is installed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, installed := loginservice.Status()
			if !installed {
				fmt.Println("resume-at-login: not installed")
				return &exitCodeError{code: exitNotRunning}
			}
			fmt.Printf("resume-at-login: installed (%s)\n", path)
			return nil
		},
	}
}

// newSandboxSyncResumeAllCmd is what the login service runs. It starts every
// persisted session that is not halted; a halted one stays halted, because a
// halt is a durable state a reboot does not resolve.
func newSandboxSyncResumeAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "resume-all",
		Short:  "Start every persisted session that is not halted",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			states, err := loadSessions()
			if err != nil {
				return err
			}
			started, skipped := 0, 0
			for _, state := range states {
				if state.Status == session.StatusHalted {
					skipped++
					continue
				}
				if sessionRunning(state) {
					continue
				}
				if err := startDetached(cmd, state); err != nil {
					fmt.Fprintf(os.Stderr, "session %s: %v\n", state.Name, err)
					continue
				}
				started++
			}
			fmt.Fprintf(os.Stderr, "started %d session(s), left %d halted\n", started, skipped)
			return nil
		},
	}
}

// unsupportedLoginService turns the platform error into something actionable
// rather than a bare failure.
func describeLoginServiceError(err error) error {
	if errors.Is(err, loginservice.ErrUnsupported) {
		return fmt.Errorf("%w; start sessions with `agyn sandbox sync start` instead", err)
	}
	return err
}
