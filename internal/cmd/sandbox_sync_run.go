package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	"github.com/agynio/agyn-cli/internal/sync/session"
	"github.com/agynio/agyn-cli/internal/sync/tree"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// daemonEnv marks the re-executed child. Only `sandbox sync` and an explicit
// daemon start bring the daemon up; no other command in the CLI does.
const daemonEnv = "AGYN_SYNC_DAEMON"

func newSandboxSyncStartCmd() *cobra.Command {
	var localPath, remotePath string
	var foreground bool
	var organizationID string

	cmd := &cobra.Command{
		Use:   "start [NAME]",
		Short: "Start syncing a local directory with a sandbox directory",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			orgID, err := clients.resolveOrganizationID(ctx, organizationID)
			if err != nil {
				return err
			}

			running, err := ensureRunningSandbox(ctx, clients, orgID, positional)
			if err != nil {
				return err
			}
			if foreground {
				state, err := createSyncSession(running, localPath, remotePath)
				if err != nil {
					return err
				}
				store, err := openSessionStore()
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "syncing %s ⇄ %s:%s\n", state.LocalRoot, state.Sandbox, state.RemoteRoot)
				return runSession(ctx, clients, store, state, true)
			}
			return startSyncSession(cmd, clients, running, localPath, remotePath)
		},
	}
	cmd.Flags().StringVar(&localPath, "local", "", "Local directory (default the working directory)")
	cmd.Flags().StringVar(&remotePath, "remote", defaultRemoteRoot, "Sandbox directory")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run in this process with no daemon, for debugging and non-interactive use")
	cmd.Flags().StringVar(&organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

// ensureRunningSandbox resolves the sandbox and brings it up. An explicit start
// calls EnsureSandboxRunning because a person asking is not the same event as a
// file changing; reconnection never does.
func ensureRunningSandbox(ctx context.Context, clients *sandboxClients, orgID string, positional []string) (*agentsv1.Sandbox, error) {
	var name string
	if len(positional) == 1 {
		name = positional[0]
	}
	sandbox, err := clients.resolveSandbox(ctx, orgID, name)
	if err != nil {
		return nil, err
	}
	ensured, err := clients.agents.EnsureSandboxRunning(ctx, connect.NewRequest(&agentsv1.EnsureSandboxRunningRequest{
		Id: sandbox.GetMeta().GetId(),
	}))
	if err != nil {
		return nil, err
	}
	return waitForRunningSandbox(ctx, clients, ensured.Msg.GetSandbox())
}

// createSyncSession records a new session, refusing one whose local root would
// overlap an existing session's — two engines writing one subtree cannot be
// reconciled.
// checkSyncLocalRoot resolves the local root and refuses one that overlaps an
// existing session. It is separate from createSyncSession so `sandbox start
// --sync` can run it *before* creating a sandbox: failing afterwards leaves a
// sandbox nobody asked for, running and billable.
func checkSyncLocalRoot(localPath string) (string, error) {
	local, err := resolveLocalRoot(localPath)
	if err != nil {
		return "", err
	}
	store, err := openSessionStore()
	if err != nil {
		return "", err
	}
	existing, err := store.List()
	if err != nil {
		return "", err
	}
	for _, other := range existing {
		if !session.Overlaps(local, other.LocalRoot) {
			continue
		}
		// Two engines writing one subtree cannot be reconciled. Name the way
		// out: without it the only clue is a session the engineer may have
		// forgotten, possibly for a sandbox that no longer exists.
		if other.LocalRoot == local {
			return "", fmt.Errorf("%s is already synced by session %q (%s).\nRemove it with `agyn sandbox sync stop %s`, or sync a different directory with --sync PATH",
				local, other.Name, other.Sandbox, other.Name)
		}
		return "", fmt.Errorf("%s overlaps session %q at %s.\nTwo sessions cannot write one subtree; remove it with `agyn sandbox sync stop %s`",
			local, other.Name, other.LocalRoot, other.Name)
	}
	return local, nil
}

func createSyncSession(sandbox *agentsv1.Sandbox, localPath, remotePath string) (*session.State, error) {
	local, err := checkSyncLocalRoot(localPath)
	if err != nil {
		return nil, err
	}
	remote := strings.TrimSpace(remotePath)
	if remote == "" {
		remote = defaultRemoteRoot
	}
	store, err := openSessionStore()
	if err != nil {
		return nil, err
	}
	existing, err := store.List()
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, other := range existing {
		taken[other.Name] = true
	}
	inode, err := session.RootInode(local)
	if err != nil {
		return nil, err
	}
	state := &session.State{
		ID:             uuid.NewString(),
		Name:           session.NameFor(local, sandbox.GetName(), func(c string) bool { return taken[c] }),
		LocalRoot:      local,
		LocalRootInode: inode,
		SandboxID:      sandbox.GetMeta().GetId(),
		Sandbox:        sandbox.GetName(),
		RemoteRoot:     remote,
		Status:         session.StatusIdle,
	}
	if err := store.Save(state); err != nil {
		return nil, err
	}
	return state, nil
}

// startSyncSession creates a session and detaches it. Shared by `sync start`
// and `sandbox start --sync`.
func startSyncSession(cmd *cobra.Command, clients *sandboxClients, sandbox *agentsv1.Sandbox, localPath, remotePath string) error {
	state, err := createSyncSession(sandbox, localPath, remotePath)
	if err != nil {
		return err
	}
	return startDetached(cmd, state)
}

// runSession drives one session to completion. It is the body of both the
// foreground form and the detached daemon.
func runSession(ctx context.Context, clients *sandboxClients, store *session.Store, state *session.State, verbose bool) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := writeSessionPID(store, state); err != nil {
		return err
	}
	defer removeSessionPID(store, state)

	runner := &session.Runner{
		State: state,
		Store: store,
		Dial: func(ctx context.Context) (*session.Connection, error) {
			current, err := clients.agents.GetSandbox(ctx, connect.NewRequest(&agentsv1.GetSandboxRequest{
				Ref: &agentsv1.GetSandboxRequest_Id{Id: state.SandboxID},
			}))
			if err != nil {
				return nil, err
			}
			// Reconnect never restarts a stopped sandbox — a background file
			// change must not start billable compute.
			if current.Msg.GetSandbox().GetStatus() != agentsv1.SandboxStatus_SANDBOX_STATUS_RUNNING {
				return nil, fmt.Errorf("sandbox %s is not running", state.Sandbox)
			}
			conn, endpoint, err := dialSync(ctx, clients, current.Msg.GetSandbox(), state.RemoteRoot)
			if err != nil {
				return nil, err
			}
			return &session.Connection{
				Client:      endpoint,
				Diagnostics: conn.Diagnostics,
				Close:       conn.Close,
			}, nil
		},
		Sandbox: func(ctx context.Context) (session.SandboxState, error) {
			current, err := clients.agents.GetSandbox(ctx, connect.NewRequest(&agentsv1.GetSandboxRequest{
				Ref: &agentsv1.GetSandboxRequest_Id{Id: state.SandboxID},
			}))
			if err != nil {
				return "", err
			}
			switch current.Msg.GetSandbox().GetStatus() {
			case agentsv1.SandboxStatus_SANDBOX_STATUS_RUNNING:
				return session.SandboxRunning, nil
			case agentsv1.SandboxStatus_SANDBOX_STATUS_TERMINATED:
				return session.SandboxTerminated, nil
			default:
				return session.SandboxStopped, nil
			}
		},
	}
	if verbose {
		runner.Log = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}
	err := runner.Run(ctx)
	if halt := (*session.Halt)(nil); err != nil && asHalt(err, &halt) {
		writeSentinel(state, halt)
		notify(fmt.Sprintf("agyn sync halted: %s", halt.Detail))
	}
	return err
}

func asHalt(err error, target **session.Halt) bool {
	halt, ok := err.(*session.Halt)
	if ok {
		*target = halt
	}
	return ok
}

// startDetached re-executes the CLI in a new session with no controlling
// terminal, so sync outlives the terminal that started it.
func startDetached(cmd *cobra.Command, state *session.State) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logPath, err := daemonLogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devNull.Close()

	child := exec.Command(executable, "sandbox", "sync", "run", state.ID)
	child.Env = append(os.Environ(), daemonEnv+"=1")
	child.Stdin = devNull
	child.Stdout = logFile
	child.Stderr = logFile
	// A new session detaches from the terminal and reparents to init, so
	// closing the window does not take the session with it.
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return err
	}
	if err := child.Process.Release(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "syncing %s ⇄ %s:%s (session %s)\n",
		state.LocalRoot, state.Sandbox, state.RemoteRoot, state.Name)
	fmt.Fprintf(os.Stderr, "logs: %s\n", logPath)
	return nil
}

// newSandboxSyncRunCmd is the detached child's entry point. Hidden: it is an
// implementation detail of starting a session, not a command to type.
func newSandboxSyncRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run SESSION_ID",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			store, err := openSessionStore()
			if err != nil {
				return err
			}
			state, err := store.Load(args[0])
			if err != nil {
				return err
			}
			return runSession(cmd.Context(), clients, store, state, true)
		},
	}
}

func openSessionStore() (*session.Store, error) {
	root, err := session.DefaultRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return session.NewStore(root), nil
}

func daemonLogPath() (string, error) {
	root, err := session.DefaultRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "daemon.log"), nil
}

func resolveLocalRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		working, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = working
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", resolved)
	}
	return resolved, nil
}

// writeSentinel puts the halt where it will be noticed: in the editor tree and
// in git status. It is the only thing sync leaves in the engineer's directory.
func writeSentinel(state *session.State, halt *session.Halt) {
	path := filepath.Join(state.LocalRoot, "AGYN-SYNC-HALTED.txt")
	body := fmt.Sprintf(""+
		"agyn sync halted\n\n"+
		"session:  %s\n"+
		"sandbox:  %s\n"+
		"reason:   %s\n"+
		"detail:   %s\n\n"+
		"Nothing has been applied. Run `agyn sandbox sync status` for the recovery.\n",
		state.Name, state.Sandbox, halt.Reason, halt.Detail)
	_ = os.WriteFile(path, []byte(body), 0o644)
}

func clearSentinel(state *session.State) {
	_ = os.Remove(filepath.Join(state.LocalRoot, "AGYN-SYNC-HALTED.txt"))
	_ = os.RemoveAll(tree.StagingDir(state.LocalRoot))
}
