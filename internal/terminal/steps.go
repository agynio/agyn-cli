package terminal

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// Steps renders a sequence of long operations as one line each.
//
// The operations behind `agyn local` take minutes and are driven by tools whose
// own output answers a different question than the user asked: a Helm release
// listing, eight identical admission warnings, a byte counter. Those go to a
// log. What reaches the terminal is what is happening now, and what it cost.
//
// On a terminal the line for the step in progress is animated and carries the
// detail it is waiting on, so a minute of work is visibly work rather than
// possibly a hang. Anywhere else — a pipe, a CI log — each step prints once, on
// completion, with the same content and no control characters.
type Steps struct {
	out     io.Writer
	animate bool

	mu      sync.Mutex
	active  *Step
	stopped chan struct{}
}

// Step is one operation in progress. Its methods are safe to call from another
// goroutine, which is what lets a poller update the detail while the work runs.
type Step struct {
	steps   *Steps
	title   string
	detail  string
	started time.Time
	frame   int
	done    bool
}

const (
	symbolDone = "✓"
	symbolFail = "✗"
	symbolSkip = "·"
)

// spinnerFrames is the braille cycle: one cell, so the title never shifts.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewSteps writes to out, animating only when out is a terminal.
func NewSteps(out io.Writer) *Steps {
	return &Steps{out: out, animate: IsTerminalWriter(out)}
}

// NewPlainSteps never animates. For --debug, where the tool output the steps
// exist to hide is streaming to the same terminal: a spinner redrawing over it
// would corrupt both.
func NewPlainSteps(out io.Writer) *Steps {
	return &Steps{out: out}
}

// IsTerminalWriter reports whether a writer is an interactive terminal.
// Anything that is not a file — a buffer in a test, a pipe in CI — is not.
func IsTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// Start begins a step. Exactly one step is in progress at a time: a prompt or a
// sudo password arriving under a spinner is unreadable, so callers finish the
// step before asking anything.
func (s *Steps) Start(title string) *Step {
	s.mu.Lock()
	defer s.mu.Unlock()

	step := &Step{steps: s, title: title, started: time.Now()}
	s.active = step
	if !s.animate {
		return step
	}

	s.render(step, "")
	stopped := make(chan struct{})
	s.stopped = stopped
	go s.animateLoop(step, stopped)
	return step
}

// Report prints a finished step that had no visible duration, or whose work
// wrote to the terminal itself — the sudo prompt of a trust-store install is
// not something to animate over.
func (s *Steps) Report(title, detail string) {
	s.instant(title, detail, symbolDone, "\x1b[32m")
}

// Skipped prints a step that was not run, naming why — and, by convention, the
// command that would run it later.
func (s *Steps) Skipped(title, detail string) {
	s.instant(title, detail, symbolSkip, "\x1b[2m")
}

// Failed prints a step that failed before it started running, and returns the
// error so a caller can write `return steps.Failed(...)`.
func (s *Steps) Failed(title string, err error) error {
	s.instant(title, err.Error(), symbolFail, "\x1b[31m")
	return err
}

func (s *Steps) instant(title, detail, symbol, color string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// started, or the zero time is measured against now and the line ends in a
	// duration of 153722867m -- these steps took no time, which is why they are
	// reported rather than run.
	s.finish(&Step{steps: s, title: title, detail: detail, started: time.Now()}, symbol, color)
}

// Note prints a line that is not a step: something the user needs to know that
// no step is responsible for.
func (s *Steps) Note(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.out, "  %s\n", text)
}

// Rule draws a divider, separating the account of what happened from what to do
// with it.
func (s *Steps) Rule() {
	s.mu.Lock()
	defer s.mu.Unlock()
	width := terminalWidth(s.out) - 4
	if width > 56 {
		width = 56
	}
	if width < 8 {
		width = 8
	}
	line := "  " + strings.Repeat("─", width)
	if s.animate {
		line = "  \x1b[2m" + strings.Repeat("─", width) + "\x1b[0m"
	}
	fmt.Fprintln(s.out, "\n"+line)
}

// Detail prints a labelled line under the call to action -- what to do once the
// link is open, which is a different question from what just happened.
func (s *Steps) Detail(label, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.animate {
		fmt.Fprintf(s.out, "  %-28s \x1b[1m%s\x1b[0m\n", label, value)
		return
	}
	fmt.Fprintf(s.out, "  %-28s %s\n", label, value)
}

// CallToAction closes a successful run with the one thing to do next. A
// finished install owes the reader a next step, and three links are a menu.
func (s *Steps) CallToAction(label, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.animate {
		fmt.Fprintf(s.out, "\n  %s  \x1b[1;4m%s\x1b[0m\n", label, url)
		return
	}
	fmt.Fprintf(s.out, "\n  %s  %s\n", label, url)
}

// Detail replaces the trailing context of a running step — the bytes so far,
// the workloads not yet ready. Ignored once the step has finished, so a poller
// racing the step's completion cannot resurrect its line.
func (st *Step) Detail(detail string) {
	st.steps.mu.Lock()
	defer st.steps.mu.Unlock()
	if st.done {
		return
	}
	st.detail = detail
	if st.steps.animate {
		st.steps.render(st, spinnerFrames[st.frame])
	}
}

// Done marks the step complete. detail may be empty.
func (st *Step) Done(detail string) {
	st.finish(detail, symbolDone, "\x1b[32m")
}

// Skip marks a step that was not run, naming why — and, by convention, the
// command that would run it later.
func (st *Step) Skip(detail string) {
	st.finish(detail, symbolSkip, "\x1b[2m")
}

// Fail marks the step failed and returns the error, so a caller can write
// `return step.Fail(err)` and be sure the line was closed.
func (st *Step) Fail(err error) error {
	st.finish(err.Error(), symbolFail, "\x1b[31m")
	return err
}

// finish closes the step with detail as its last word -- empty meaning none,
// rather than whatever progress happened to scroll by last. A step that spent a
// minute reporting where it had got to should not end on the final frame of
// that: "Pointing the platform at port 2497  agyn-sandboxes -> ..." says less
// than the title alone.
func (st *Step) finish(detail, symbol, color string) {
	st.steps.mu.Lock()
	defer st.steps.mu.Unlock()
	if st.done {
		return
	}
	st.detail = detail
	st.steps.finish(st, symbol, color)
}

// finish closes a step's line. Callers hold the lock.
func (s *Steps) finish(step *Step, symbol, color string) {
	step.done = true
	// Only the running step owns the animation. An instantly-reported step is
	// not it, and stopping the animation on its behalf would leave the running
	// step's line frozen mid-spin and never closed.
	if s.active == step {
		if s.stopped != nil {
			close(s.stopped)
			s.stopped = nil
		}
		s.active = nil
	}

	line := "  " + symbol + " " + step.title
	if s.animate {
		line = "  " + color + symbol + "\x1b[0m " + step.title
	}
	if suffix := step.suffix(s.animate); suffix != "" {
		line += suffix
	}
	if s.animate {
		fmt.Fprint(s.out, "\r\x1b[2K")
	}
	fmt.Fprintln(s.out, line)
}

// animateLoop redraws the running step until it finishes.
func (s *Steps) animateLoop(step *Step, stopped chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stopped:
			return
		case <-ticker.C:
			s.mu.Lock()
			if step.done {
				s.mu.Unlock()
				return
			}
			step.frame = (step.frame + 1) % len(spinnerFrames)
			s.render(step, spinnerFrames[step.frame])
			s.mu.Unlock()
		}
	}
}

// render draws the in-progress line. Callers hold the lock.
func (s *Steps) render(step *Step, frame string) {
	if frame == "" {
		frame = spinnerFrames[0]
	}
	line := "  \x1b[36m" + frame + "\x1b[0m " + step.title + step.suffix(true)
	// Truncated rather than wrapped: a wrapped line leaves the carriage return
	// on the wrong row and the next redraw paints over the wrong text.
	fmt.Fprint(s.out, "\r\x1b[2K"+truncate(line, terminalWidth(s.out)-1))
}

// suffix is the dim trailing context: the detail, then how long the step took.
func (st *Step) suffix(color bool) string {
	parts := make([]string, 0, 2)
	if st.detail != "" {
		parts = append(parts, st.detail)
	}
	if elapsed := formatElapsed(time.Since(st.started)); st.done && elapsed != "" {
		parts = append(parts, elapsed)
	}
	if len(parts) == 0 {
		return ""
	}
	text := "  " + strings.Join(parts, "  ")
	if color {
		return "\x1b[2m" + text + "\x1b[0m"
	}
	return text
}

// formatElapsed reports a duration worth reporting. Under a second is not: it
// says nothing, and printing "0s" beside every fast step is noise.
func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// truncate cuts a line to a printable width, counting runes rather than bytes
// and ignoring the escape sequences, which occupy no columns.
func truncate(line string, width int) string {
	if width <= 0 {
		return line
	}
	visible, escape := 0, false
	for index, r := range line {
		switch {
		case escape:
			if r == 'm' {
				escape = false
			}
		case r == '\x1b':
			escape = true
		default:
			visible++
			if visible > width {
				return line[:index] + "…\x1b[0m"
			}
		}
	}
	return line
}

func terminalWidth(out io.Writer) int {
	file, ok := out.(*os.File)
	if !ok {
		return defaultCols
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return defaultCols
	}
	return width
}
