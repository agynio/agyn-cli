package session

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/agynio/agyn-cli/internal/sync/client"
)

// Connection is one attached endpoint session. The runner owns its lifetime and
// drops it on any failure, because watch state is never trusted across a
// disconnect.
type Connection struct {
	Client      *client.Client
	Diagnostics func() string
	Close       func() error
}

// SandboxState is what the runner needs to know about the sandbox between
// cycles, without depending on the Gateway client directly.
type SandboxState string

const (
	SandboxRunning    SandboxState = "running"
	SandboxStopped    SandboxState = "stopped"
	SandboxTerminated SandboxState = "terminated"
)

// Runner drives one session: connect, cycle, classify any failure, reconnect.
type Runner struct {
	State *State
	Store *Store

	// Dial attaches a fresh endpoint session. Every reconnect performs a full
	// rescan, so nothing is carried across.
	Dial func(context.Context) (*Connection, error)
	// Sandbox reports the sandbox's current state, which is what distinguishes
	// a pause from a halt when the stream breaks.
	Sandbox func(context.Context) (SandboxState, error)

	// Interval is how often a connected session cycles.
	Interval time.Duration
	// Log receives one line per state change. Never per cycle: a quiet session
	// should be quiet.
	Log func(format string, args ...any)

	backoff time.Duration
}

const (
	defaultInterval = 3 * time.Second
	minBackoff      = 1 * time.Second
	maxBackoff      = 60 * time.Second
	// pausedPoll is how often a paused session checks whether the sandbox came
	// back. It never restarts one: a background file change must not start
	// billable compute.
	pausedPoll = 30 * time.Second
)

// Run drives the session until the context is cancelled or it halts. A halt is
// durable and safe to remain in indefinitely, so Run returns rather than
// retrying.
func (r *Runner) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = defaultInterval
	}
	if r.Log == nil {
		r.Log = func(string, ...any) {}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := r.attachAndSync(ctx)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, context.Canceled):
			return nil
		}

		var halt *Halt
		if errors.As(err, &halt) {
			r.setStatus(StatusHalted, string(halt.Reason)+": "+halt.Detail)
			r.Log("halted: %s", halt.Error())
			return halt
		}

		// Not a halt, so classify by what the sandbox is doing rather than by
		// the transport error, which cannot tell a stop from a network drop.
		state, stateErr := r.sandboxState(ctx)
		switch {
		case stateErr == nil && state == SandboxTerminated:
			r.setStatus(StatusHalted, "the sandbox was terminated")
			r.Log("halted: the sandbox was terminated")
			return &Halt{Reason: HaltSandboxGone, Detail: "the sandbox was terminated"}
		case stateErr == nil && state == SandboxStopped:
			// Pause, and never restart it. Resumes when the engineer next
			// connects.
			r.setStatus(StatusPaused, "the sandbox is stopped")
			r.Log("paused: the sandbox is stopped")
			if !sleep(ctx, pausedPoll) {
				return nil
			}
			r.backoff = 0
			continue
		}

		r.Log("reconnecting: %v", err)
		if !sleep(ctx, r.nextBackoff()) {
			return nil
		}
	}
}

// attachAndSync holds one connection and cycles on it until something breaks.
func (r *Runner) attachAndSync(ctx context.Context) error {
	connection, err := r.Dial(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if connection.Close != nil {
			_ = connection.Close()
		}
	}()

	r.backoff = 0
	r.setStatus(StatusIdle, "")

	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cycle := &Cycle{State: r.State, Store: r.Store, Endpoint: connection.Client}
		outcome, err := cycle.Run()
		if err != nil {
			diagnostics := ""
			if connection.Diagnostics != nil {
				diagnostics = connection.Diagnostics()
			}
			if diagnostics != "" {
				r.Log("sandbox: %s", diagnostics)
			}
			// An endpoint that wrote to stderr and then closed the stream
			// before its first exchange did not fail in transit — it ran and
			// refused. Retrying cannot fix a binary that does not understand
			// the command it was given, and a session that reconnects forever
			// while reporting itself idle is worse than one that says why it
			// stopped.
			if first && diagnostics != "" {
				return &Halt{
					Reason: HaltVersionGap,
					Detail: fmt.Sprintf("the in-sandbox endpoint refused to start: %s", diagnostics),
				}
			}
			return err
		}
		first = false
		if outcome.AppliedLocal > 0 || outcome.AppliedRemote > 0 {
			r.Log("synced %d local, %d remote", outcome.AppliedLocal, outcome.AppliedRemote)
		}
		if len(outcome.Conflicts) > 0 {
			r.setStatus(StatusIdle, "conflicts need resolving")
		} else {
			r.setStatus(StatusIdle, "")
		}
		if !sleep(ctx, r.Interval) {
			return nil
		}
	}
}

func (r *Runner) sandboxState(ctx context.Context) (SandboxState, error) {
	if r.Sandbox == nil {
		return SandboxRunning, nil
	}
	return r.Sandbox(ctx)
}

func (r *Runner) setStatus(status Status, note string) {
	r.State.Status = status
	r.State.StatusNote = note
	_ = r.Store.Save(r.State)
}

// nextBackoff grows exponentially with jitter, so a proxy restart does not
// bring every session back at the same instant.
func (r *Runner) nextBackoff() time.Duration {
	if r.backoff == 0 {
		r.backoff = minBackoff
	} else {
		r.backoff *= 2
		if r.backoff > maxBackoff {
			r.backoff = maxBackoff
		}
	}
	jitter := time.Duration(rand.Int63n(int64(r.backoff / 2)))
	return r.backoff/2 + jitter
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
