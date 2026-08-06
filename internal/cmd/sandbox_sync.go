package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/agynio/agyn-cli/internal/sync/endpoint"
	"github.com/spf13/cobra"
)

// workspaceDirEnv is set on every sandbox container by the orchestrator. It is
// what confines a sync root: the Gateway validates the path lexically but has
// no mount data for a container, so only this process can check where the root
// actually lands.
const workspaceDirEnv = "WORKSPACE_DIR"

func newSandboxSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Keep a local directory and a sandbox directory reconciled",
	}
	cmd.AddCommand(newSandboxSyncServeCmd())
	cmd.AddCommand(newSandboxSyncStartCmd())
	cmd.AddCommand(newSandboxSyncRunCmd())
	cmd.AddCommand(newSandboxSyncListCmd())
	cmd.AddCommand(newSandboxSyncStatusCmd())
	cmd.AddCommand(newSandboxSyncStopCmd())
	cmd.AddCommand(newSandboxSyncResumeCmd())
	cmd.AddCommand(newSandboxSyncResolveCmd())
	cmd.AddCommand(newSandboxSyncAcceptDeletionsCmd())
	cmd.AddCommand(newSandboxSyncResetCmd())
	cmd.AddCommand(newSandboxSyncUndeleteCmd())
	cmd.AddCommand(newSandboxSyncDaemonCmd())
	cmd.AddCommand(newSandboxSyncResumeAllCmd())
	return cmd
}

func newSandboxSyncServeCmd() *cobra.Command {
	var root string
	var workspace string

	cmd := &cobra.Command{
		Use:    "serve",
		Short:  "Run the in-sandbox sync endpoint",
		Hidden: true,
		Long: "The in-sandbox endpoint, launched by the platform inside the container.\n" +
			"It speaks the sync protocol on stdin and stdout and is never run by hand.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			protocol, err := claimStdout()
			if err != nil {
				return err
			}
			if workspace == "" {
				workspace = os.Getenv(workspaceDirEnv)
			}
			return endpoint.Serve(endpoint.Options{
				Root:      root,
				Workspace: workspace,
				Version:   version,
			}, os.Stdin, protocol)
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Directory to serve (required)")
	cmd.Flags().StringVar(&workspace, "workspace", "",
		fmt.Sprintf("Confine --root to this mount (default $%s)", workspaceDirEnv))
	_ = cmd.MarkFlagRequired("root")
	return cmd
}

// claimStdout takes the real stdout for protocol frames and points everything
// else at stderr. Nothing but frames may reach the stream: one stray byte from
// a library writing to stdout corrupts it, and the controller has no way to
// resynchronize.
func claimStdout() (*os.File, error) {
	saved, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		return nil, fmt.Errorf("claim stdout: %w", err)
	}
	protocol := os.NewFile(uintptr(saved), "sync-protocol")
	// Both halves matter: the descriptor covers anything writing to fd 1
	// directly, and the variable covers every Go writer that reaches for
	// os.Stdout.
	if err := redirectStdoutFD(); err != nil {
		protocol.Close()
		return nil, fmt.Errorf("redirect stdout: %w", err)
	}
	os.Stdout = os.Stderr
	return protocol, nil
}
