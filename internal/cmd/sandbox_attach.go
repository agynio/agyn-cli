package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	gatewayv1 "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1"
	"github.com/agynio/agyn-cli/internal/terminal"
)

// sandboxMainContainer is the container a shell attaches to. A sandbox is the
// environment's main container plus platform sidecars; sidecars are not
// attachable.
const sandboxMainContainer = "main"

// exitCodeError carries a remote shell's exit code so the CLI can exit with it
// without printing a spurious error message.
type exitCodeError struct{ code int }

func (e *exitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// ExitCode lets the root command translate a remote exit into our own.
func (e *exitCodeError) ExitCode() int { return e.code }

// attachToSandbox waits for the workload to run, requests a terminal ticket and
// streams the session until the shell exits.
func attachToSandbox(ctx context.Context, clients *sandboxClients, sandbox *agentsv1.Sandbox) error {
	if !terminal.IsTerminal(os.Stdin) || !terminal.IsTerminal(os.Stdout) {
		return fmt.Errorf("a TTY is required; `agyn sandbox` has no non-interactive mode")
	}

	running, err := waitForRunningSandbox(ctx, clients, sandbox)
	if err != nil {
		return err
	}
	workloadID := running.GetWorkloadId()
	if workloadID == "" {
		return fmt.Errorf("sandbox %s has no workload to attach to", running.GetName())
	}

	session, err := clients.terminal.CreateTerminalSession(ctx, connect.NewRequest(&gatewayv1.CreateTerminalSessionRequest{
		WorkloadId:    workloadID,
		ContainerName: sandboxMainContainer,
	}))
	if err != nil {
		return err
	}

	return streamTerminal(ctx, clients, session.Msg.GetTicket(), session.Msg.GetWebsocketUrl())
}

// streamTerminal puts the local terminal in raw mode and bridges it to the
// remote PTY. Termios is restored on every exit path, including panics.
func streamTerminal(ctx context.Context, clients *sandboxClients, ticket, websocketURL string) error {
	tty, err := terminal.MakeRaw(os.Stdin)
	if err != nil {
		return err
	}
	defer tty.Restore()
	tty.WatchResize()

	size, err := tty.Size()
	if err != nil {
		// A missing size is not fatal; the shell can still run at the default.
		size = terminal.Size{Cols: 80, Rows: 24}
	}

	result, err := terminal.Attach(ctx, terminal.Options{
		URL:         websocketURL,
		Ticket:      ticket,
		InitialSize: size,
		Resize:      tty.Resize(),
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		HTTPClient:  clients.runContext.Clients.HTTPClient,
	})

	// Restore before writing anything of our own, so notices are not printed
	// into a raw-mode terminal.
	tty.Restore()

	if err != nil {
		return fmt.Errorf("terminal session: %w", err)
	}
	switch result.Reason {
	case terminal.ReasonCancelled:
		fmt.Fprintln(os.Stderr, "\nSession ended: the workload was stopped.")
	case terminal.ReasonError:
		fmt.Fprintln(os.Stderr, "\nSession ended: connection lost.")
	}
	if result.Code != 0 {
		return &exitCodeError{code: result.Code}
	}
	return nil
}

// waitForRunningSandbox polls until the orchestrator reports the workload
// running. Sandboxes start asynchronously, so both `start` and `connect` wait.
func waitForRunningSandbox(ctx context.Context, clients *sandboxClients, sandbox *agentsv1.Sandbox) (*agentsv1.Sandbox, error) {
	if sandbox.GetStatus() == agentsv1.SandboxStatus_SANDBOX_STATUS_RUNNING && sandbox.GetWorkloadId() != "" {
		return sandbox, nil
	}

	deadline := time.Now().Add(startWaitTimeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	id := sandbox.GetMeta().GetId()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		response, err := clients.agents.GetSandbox(ctx, connect.NewRequest(&agentsv1.GetSandboxRequest{
			Ref: &agentsv1.GetSandboxRequest_Id{Id: id},
		}))
		if err != nil {
			return nil, err
		}
		current := response.Msg.GetSandbox()

		switch current.GetStatus() {
		case agentsv1.SandboxStatus_SANDBOX_STATUS_RUNNING:
			if current.GetWorkloadId() != "" {
				return current, nil
			}
		case agentsv1.SandboxStatus_SANDBOX_STATUS_FAILED:
			return nil, fmt.Errorf("sandbox %s failed to start; run `agyn sandbox connect %s` to retry",
				current.GetName(), current.GetName())
		case agentsv1.SandboxStatus_SANDBOX_STATUS_TERMINATED:
			return nil, fmt.Errorf("sandbox %s is terminated", current.GetName())
		}

		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for the sandbox workload to start")
		}
	}
}
