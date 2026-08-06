package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agynio/agyn-cli/internal/sync/client"
)

// deadEndpoint is a client whose stream is already at EOF — what an endpoint
// that exited before answering actually looks like from this side.
func deadEndpoint() *client.Client {
	return client.New(bytes.NewReader(nil), io.Discard)
}

// An endpoint that wrote to stderr and closed before its first exchange ran and
// refused. Reconnecting cannot fix a binary that does not understand the command
// it was given, and a session that retries forever while reporting itself idle
// is worse than one that says why it stopped.
func TestEndpointThatRefusedToStartHalts(t *testing.T) {
	store := NewStore(t.TempDir())
	state := &State{ID: "s", Name: "s", LocalRoot: t.TempDir(), Status: StatusIdle}

	dials := 0
	runner := &Runner{
		State:    state,
		Store:    store,
		Interval: time.Millisecond,
		Dial: func(context.Context) (*Connection, error) {
			dials++
			return &Connection{
				Client:      deadEndpoint(),
				Diagnostics: func() string { return "Error: unknown flag: --root" },
				Close:       func() error { return nil },
			}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runner.Run(ctx)

	var halt *Halt
	if !errors.As(err, &halt) {
		t.Fatalf("expected a halt, got %v", err)
	}
	if halt.Reason != HaltVersionGap {
		t.Fatalf("expected a version-gap halt, got %s", halt.Reason)
	}
	if dials != 1 {
		t.Fatalf("expected one attempt, got %d — it retried a permanent failure", dials)
	}
	if state.Status != StatusHalted {
		t.Fatalf("session left in %s rather than halted", state.Status)
	}
}

// A failure with nothing on stderr is a transport failure: the endpoint never
// got to say anything, so retrying is the right answer.
func TestSilentFailureReconnectsRatherThanHalting(t *testing.T) {
	store := NewStore(t.TempDir())
	state := &State{ID: "s", Name: "s", LocalRoot: t.TempDir(), Status: StatusIdle}

	dials := 0
	runner := &Runner{
		State:    state,
		Store:    store,
		Interval: time.Millisecond,
		Dial: func(context.Context) (*Connection, error) {
			dials++
			if dials > 2 {
				return nil, io.EOF
			}
			return &Connection{Client: deadEndpoint(), Diagnostics: func() string { return "" }, Close: func() error { return nil }}, nil
		},
		Sandbox: func(context.Context) (SandboxState, error) { return SandboxRunning, nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = runner.Run(ctx)

	if dials < 2 {
		t.Fatalf("a silent failure was not retried; %d attempt(s)", dials)
	}
	if state.Status == StatusHalted {
		t.Fatal("a silent transport failure halted the session")
	}
}
