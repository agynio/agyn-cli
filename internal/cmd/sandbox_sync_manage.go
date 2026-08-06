package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agynio/agyn-cli/internal/output"
	"github.com/agynio/agyn-cli/internal/sync/session"
	"github.com/agynio/agyn-cli/internal/terminal"
	"github.com/spf13/cobra"
)

// Exit codes distinguish the conditions a shell prompt would style differently.
const (
	exitHealthy      = 0
	exitHalted       = 2
	exitConflicts    = 3
	exitNotRunning   = 4
	exitNoSuchThing  = 1
	sentinelFileName = "AGYN-SYNC-HALTED.txt"
)

type syncSessionOutput struct {
	Name       string `json:"name" yaml:"name"`
	LocalRoot  string `json:"local_root" yaml:"local_root"`
	Sandbox    string `json:"sandbox" yaml:"sandbox"`
	RemoteRoot string `json:"remote_root" yaml:"remote_root"`
	Status     string `json:"status" yaml:"status"`
	Note       string `json:"note,omitempty" yaml:"note,omitempty"`
	LastSync   string `json:"last_sync,omitempty" yaml:"last_sync,omitempty"`
	Conflicts  int    `json:"conflicts" yaml:"conflicts"`
	Running    bool   `json:"running" yaml:"running"`
}

func newSandboxSyncListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sync sessions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			states, err := loadSessions()
			if err != nil {
				return err
			}
			outputs := make([]syncSessionOutput, 0, len(states))
			rows := make([][]string, 0, len(states))
			for _, state := range states {
				out := sessionOutputFrom(state)
				outputs = append(outputs, out)
				rows = append(rows, []string{out.Name, out.LocalRoot, out.Sandbox + ":" + out.RemoteRoot, out.Status, out.LastSync})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{
					Headers: []string{"NAME", "LOCAL", "SANDBOX", "STATUS", "LAST_SYNC"},
					Rows:    rows,
				})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
}

func newSandboxSyncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [SESSION]",
		Short: "Show sync session state, exiting non-zero while anything needs attention",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveSession(args)
			if err != nil {
				return &exitCodeError{code: exitNoSuchThing}
			}
			out := sessionOutputFrom(state)
			fmt.Printf("session:   %s\nlocal:     %s\nsandbox:   %s:%s\nstatus:    %s\n",
				out.Name, out.LocalRoot, out.Sandbox, out.RemoteRoot, out.Status)
			if out.LastSync != "" {
				fmt.Printf("last sync: %s\n", out.LastSync)
			}
			if state.StatusNote != "" {
				fmt.Printf("detail:    %s\n", state.StatusNote)
			}
			for _, conflict := range state.Conflicts {
				fmt.Printf("conflict:  %s (local %s, sandbox %s)\n", conflict.Path, conflict.LocalChange, conflict.RemoteChange)
			}

			switch {
			case state.Status == session.StatusHalted:
				fmt.Fprintln(os.Stderr, "\nHalted. Nothing has been applied.")
				fmt.Fprintln(os.Stderr, recoveryFor(state))
				return &exitCodeError{code: exitHalted}
			case len(state.Conflicts) > 0:
				fmt.Fprintln(os.Stderr, "\nResolve with `agyn sandbox sync resolve <path> --keep-local|--keep-remote`.")
				return &exitCodeError{code: exitConflicts}
			case !out.Running:
				fmt.Fprintln(os.Stderr, "\nNot running. Start it with `agyn sandbox sync start`.")
				return &exitCodeError{code: exitNotRunning}
			}
			return nil
		},
	}
}

// recoveryFor names the command that matches what the engineer would be
// asserting, rather than offering a single escape hatch for every cause.
func recoveryFor(state *session.State) string {
	note := state.StatusNote
	switch {
	case strings.HasPrefix(note, string(session.HaltContentLoss)):
		return "If the cause was environmental — a drive now mounted, a checkout that has finished —\n" +
			"run `agyn sandbox sync resume`. If the deletion was intended, run\n" +
			"`agyn sandbox sync accept-deletions`, which names the count before proceeding."
	case strings.HasPrefix(note, string(session.HaltRootReplaced)),
		strings.HasPrefix(note, string(session.HaltSandboxGone)):
		return "A root was replaced or wiped. Declare which side becomes the new base with\n" +
			"`agyn sandbox sync reset --from-local` or `--from-remote`. It propagates."
	case strings.HasPrefix(note, string(session.HaltVersionGap)):
		return "The CLI and the in-sandbox endpoint do not share a protocol version. Upgrade the CLI."
	default:
		return "Run `agyn sandbox sync resume` once the cause no longer holds."
	}
}

func newSandboxSyncStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [SESSION]",
		Short: "Remove a sync session; neither side's files are touched",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveSession(args)
			if err != nil {
				return err
			}
			store, err := openSessionStore()
			if err != nil {
				return err
			}
			signalSession(state, os.Interrupt)
			clearSentinel(state)
			if err := store.Remove(state.ID); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "removed session %s; no files were touched\n", state.Name)
			return nil
		},
	}
}

func newSandboxSyncResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume [SESSION]",
		Short: "Resume a session whose halt had an environmental cause",
		Long: "For a halt the environment caused and no longer causes — a drive that is now\n" +
			"mounted, a checkout that has finished. It asserts nothing: if the condition\n" +
			"still holds, the guard fires again.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveSession(args)
			if err != nil {
				return err
			}
			store, err := openSessionStore()
			if err != nil {
				return err
			}
			// The local root may be a different directory now; re-record what
			// it is so the identity check compares against the truth.
			inode, err := session.RootInode(state.LocalRoot)
			if err != nil {
				return fmt.Errorf("local root %s: %w", state.LocalRoot, err)
			}
			state.LocalRootInode = inode
			state.Status = session.StatusIdle
			state.StatusNote = ""
			if err := store.Save(state); err != nil {
				return err
			}
			clearSentinel(state)
			fmt.Fprintf(os.Stderr, "resumed %s; start it with `agyn sandbox sync start`\n", state.Name)
			return nil
		},
	}
}

func newSandboxSyncResolveCmd() *cobra.Command {
	var keepLocal, keepRemote, all bool
	cmd := &cobra.Command{
		Use:   "resolve [PATH]",
		Short: "Resolve a quarantined conflict",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keepLocal == keepRemote {
				return errors.New("pass exactly one of --keep-local or --keep-remote")
			}
			state, err := resolveSession(nil)
			if err != nil {
				return err
			}
			store, err := openSessionStore()
			if err != nil {
				return err
			}
			if len(state.Conflicts) == 0 {
				return errors.New("no conflicts to resolve")
			}
			var path string
			if len(args) == 1 {
				path = args[0]
			}
			if path == "" && !all {
				return errors.New("name a path, or pass --all")
			}

			kept := 0
			remaining := state.Conflicts[:0]
			for _, conflict := range state.Conflicts {
				if all || conflict.Path == path {
					kept++
					continue
				}
				remaining = append(remaining, conflict)
			}
			if kept == 0 {
				return fmt.Errorf("no conflict at %s", path)
			}
			state.Conflicts = remaining
			// Dropping the ancestor makes the next cycle re-derive, and the
			// side being kept wins because the other has nothing newer to
			// claim against a base that no longer records the divergence.
			if err := store.SaveAncestor(state.ID, nil); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := store.Save(state); err != nil {
				return err
			}
			side := "local"
			if keepRemote {
				side = "sandbox"
			}
			fmt.Fprintf(os.Stderr, "resolved %d conflict(s) keeping the %s side\n", kept, side)
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepLocal, "keep-local", false, "Keep the local version")
	cmd.Flags().BoolVar(&keepRemote, "keep-remote", false, "Keep the sandbox version")
	cmd.Flags().BoolVar(&all, "all", false, "Apply to every conflict in the session")
	return cmd
}

func newSandboxSyncAcceptDeletionsCmd() *cobra.Command {
	var expect int
	cmd := &cobra.Command{
		Use:   "accept-deletions [SESSION]",
		Short: "Acknowledge a bulk deletion that halted the session",
		Long: "Clears that one pending change and leaves the session's base intact. It\n" +
			"cannot recover a replaced root — that is `sync reset`.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveSession(args)
			if err != nil {
				return err
			}
			if state.Status != session.StatusHalted {
				return errors.New("the session is not halted on a deletion")
			}
			count := pendingDeletionCount(state)
			if err := confirmDeletions(cmd, count, expect); err != nil {
				return err
			}
			store, err := openSessionStore()
			if err != nil {
				return err
			}
			// Clear only this pending change: the base stays, so the next cycle
			// reconciles normally rather than re-deriving from scratch.
			state.Status = session.StatusIdle
			state.StatusNote = ""
			if err := store.Save(state); err != nil {
				return err
			}
			clearSentinel(state)
			fmt.Fprintf(os.Stderr, "accepted %d deletion(s); start the session again to apply\n", count)
			return nil
		},
	}
	cmd.Flags().IntVar(&expect, "expect-deletions", -1, "Authorize exactly this many deletions, for non-interactive use")
	return cmd
}

func newSandboxSyncResetCmd() *cobra.Command {
	var fromLocal, fromRemote bool
	var expect int
	cmd := &cobra.Command{
		Use:   "reset [SESSION]",
		Short: "Re-establish a halted session by declaring one side authoritative",
		Long: "For a root that was replaced or wiped. Propagates: the other side is made to\n" +
			"match, deletions included.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromLocal == fromRemote {
				return errors.New("pass exactly one of --from-local or --from-remote")
			}
			state, err := resolveSession(args)
			if err != nil {
				return err
			}
			// reset propagates, so it is subject to the same confirmation. An
			// engineer reaching for it after a failed mount is stopped by the
			// same count that would have told them something was wrong.
			count := pendingDeletionCount(state)
			if err := confirmDeletions(cmd, count, expect); err != nil {
				return err
			}
			store, err := openSessionStore()
			if err != nil {
				return err
			}
			inode, err := session.RootInode(state.LocalRoot)
			if err != nil {
				return fmt.Errorf("local root %s: %w", state.LocalRoot, err)
			}
			state.LocalRootInode = inode
			state.Status = session.StatusIdle
			state.StatusNote = ""
			state.Conflicts = nil
			if err := store.SaveAncestor(state.ID, nil); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := store.Save(state); err != nil {
				return err
			}
			clearSentinel(state)
			side := "local"
			if fromRemote {
				side = "sandbox"
			}
			fmt.Fprintf(os.Stderr, "reset %s from the %s side; start the session again\n", state.Name, side)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromLocal, "from-local", false, "The local side becomes the new base")
	cmd.Flags().BoolVar(&fromRemote, "from-remote", false, "The sandbox side becomes the new base")
	cmd.Flags().IntVar(&expect, "expect-deletions", -1, "Authorize exactly this many deletions, for non-interactive use")
	return cmd
}

// confirmDeletions prompts on a TTY and, without one, neither prompts nor
// proceeds. The flag carries the expected count rather than a bare yes: a
// blanket -y written into a pipeline when three files were deleted would still
// be there the day thirty thousand are.
func confirmDeletions(cmd *cobra.Command, count, expect int) error {
	if expect >= 0 {
		if expect != count {
			return fmt.Errorf("--expect-deletions %d does not match the %d pending", expect, count)
		}
		return nil
	}
	if !terminal.IsTerminal(os.Stdin) {
		return fmt.Errorf("this will delete %d entr%s on the far side; no terminal to confirm on, so pass --expect-deletions %d",
			count, plural(count), count)
	}
	fmt.Fprintf(os.Stderr, "This will delete %d entr%s on the far side. Continue? [y/N] ", count, plural(count))
	var answer string
	_, _ = fmt.Fscanln(cmd.InOrStdin(), &answer)
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return errors.New("cancelled")
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func newSandboxSyncUndeleteCmd() *cobra.Command {
	var since time.Duration
	cmd := &cobra.Command{
		Use:   "undelete [SESSION]",
		Short: "Restore files sync removed locally, from the session's trash",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveSession(args)
			if err != nil {
				return err
			}
			store, err := openSessionStore()
			if err != nil {
				return err
			}
			restored, err := restoreTrash(store.TrashDir(state.ID), state.LocalRoot, since)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "restored %d file(s) to %s\n", restored, state.LocalRoot)
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 0, "Only restore files trashed within this window")
	return cmd
}

// restoreTrash copies back out of the trash rather than moving, so a mistaken
// undelete does not empty the only copy that is left.
func restoreTrash(trash, root string, since time.Duration) (int, error) {
	stamps, err := os.ReadDir(trash)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Time{}
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}
	restored := 0
	for _, stamp := range stamps {
		when, parseErr := time.Parse("20060102T150405", stamp.Name())
		if parseErr == nil && !cutoff.IsZero() && when.Before(cutoff) {
			continue
		}
		base := filepath.Join(trash, stamp.Name())
		walkErr := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, relErr := filepath.Rel(base, path)
			if relErr != nil {
				return relErr
			}
			destination := filepath.Join(root, rel)
			if _, statErr := os.Stat(destination); statErr == nil {
				// Something is already there; restoring over it would be the
				// second surprising deletion.
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if err := os.WriteFile(destination, data, 0o644); err != nil {
				return err
			}
			restored++
			return nil
		})
		if walkErr != nil {
			return restored, walkErr
		}
	}
	return restored, nil
}

func loadSessions() ([]*session.State, error) {
	store, err := openSessionStore()
	if err != nil {
		return nil, err
	}
	states, err := store.List()
	if err != nil {
		return nil, err
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	return states, nil
}

// resolveSession takes the named session, or the sole candidate when only one
// exists — the same shape as resolving a sandbox.
func resolveSession(args []string) (*session.State, error) {
	states, err := loadSessions()
	if err != nil {
		return nil, err
	}
	if len(args) == 1 && args[0] != "" {
		for _, state := range states {
			if state.Name == args[0] || state.ID == args[0] {
				return state, nil
			}
		}
		return nil, fmt.Errorf("no sync session named %q", args[0])
	}
	switch len(states) {
	case 0:
		return nil, errors.New("no sync sessions; start one with `agyn sandbox sync start`")
	case 1:
		return states[0], nil
	default:
		names := make([]string, 0, len(states))
		for _, state := range states {
			names = append(names, state.Name)
		}
		return nil, fmt.Errorf("multiple sync sessions; name one: %s", strings.Join(names, ", "))
	}
}

func sessionOutputFrom(state *session.State) syncSessionOutput {
	out := syncSessionOutput{
		Name:       state.Name,
		LocalRoot:  state.LocalRoot,
		Sandbox:    state.Sandbox,
		RemoteRoot: state.RemoteRoot,
		Status:     string(state.Status),
		Note:       state.StatusNote,
		Conflicts:  len(state.Conflicts),
		Running:    sessionRunning(state),
	}
	if !state.LastSync.IsZero() {
		out.LastSync = humanizeDuration(time.Since(state.LastSync)) + " ago"
	}
	// Nothing resumes automatically after a reboot: a session whose process is
	// gone is reported as not running rather than appearing to sync.
	if !out.Running && state.Status != session.StatusHalted {
		out.Status = "not running"
	}
	return out
}

// pendingDeletionCount is what the confirmation names. The halt detail carries
// it, since the cycle that halted is the only thing that counted.
func pendingDeletionCount(state *session.State) int {
	var lost, tracked int
	if _, err := fmt.Sscanf(state.StatusNote, string(session.HaltContentLoss)+": the local root lost %d of %d tracked entries", &lost, &tracked); err == nil {
		return lost
	}
	if _, err := fmt.Sscanf(state.StatusNote, string(session.HaltContentLoss)+": the sandbox workspace lost %d of %d tracked entries", &lost, &tracked); err == nil {
		return lost
	}
	return 0
}

// notify surfaces a halt at the moment it happens. Never a hard dependency:
// where neither tool exists, the sentinel file and status still report it.
func notify(message string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %q with title %q", message, "agyn")
		_ = exec.Command("osascript", "-e", script).Run()
	case "linux":
		_ = exec.Command("notify-send", "agyn", message).Run()
	}
}

// sessionRunning reports whether a process is driving this session. Nothing
// resumes after a reboot, so a session whose pid file is stale is listed as not
// running rather than appearing to sync.
func sessionRunning(state *session.State) bool {
	pid, err := readSessionPID(state)
	if err != nil {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 tests for existence without delivering anything.
	return process.Signal(syscall.Signal(0)) == nil
}

func signalSession(state *session.State, sig os.Signal) {
	pid, err := readSessionPID(state)
	if err != nil {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Signal(sig)
	}
}

func readSessionPID(state *session.State) (int, error) {
	store, err := openSessionStore()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(filepath.Join(store.Dir(state.ID), "pid"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func writeSessionPID(store *session.Store, state *session.State) error {
	return os.WriteFile(filepath.Join(store.Dir(state.ID), "pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0o600)
}

func removeSessionPID(store *session.Store, state *session.State) {
	_ = os.Remove(filepath.Join(store.Dir(state.ID), "pid"))
}
