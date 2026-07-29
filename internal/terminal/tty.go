package terminal

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// TTY owns the local terminal state for the duration of a session.
type TTY struct {
	fd       int
	oldState *term.State
	resize   chan Size
	stop     chan struct{}
}

// IsTerminal reports whether the file is an interactive terminal. `sandbox
// start` and `connect` require one — there is no non-interactive exec mode.
func IsTerminal(file *os.File) bool {
	return term.IsTerminal(int(file.Fd()))
}

// MakeRaw puts the terminal into raw mode so keystrokes, signals-as-bytes and
// escape sequences reach the remote PTY untouched. The caller must Restore.
func MakeRaw(file *os.File) (*TTY, error) {
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("not a terminal")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("set raw mode: %w", err)
	}
	return &TTY{
		fd:       fd,
		oldState: oldState,
		resize:   make(chan Size, 1),
		stop:     make(chan struct{}),
	}, nil
}

// Restore returns the terminal to its previous state. It is safe to call more
// than once, so it can be deferred and also called on a signal path.
func (t *TTY) Restore() {
	if t == nil || t.oldState == nil {
		return
	}
	_ = term.Restore(t.fd, t.oldState)
	t.oldState = nil
	select {
	case <-t.stop:
	default:
		close(t.stop)
	}
}

// defaultCols and defaultRows stand in when the terminal reports no size.
const (
	defaultCols = 80
	defaultRows = 24
)

// DefaultSize is the window size to fall back on when the local terminal
// cannot be measured at all.
func DefaultSize() Size {
	return Size{Cols: defaultCols, Rows: defaultRows}
}

// Size returns the current window size. A terminal whose window size was never
// set reports zero without erroring, and the proxy rejects a handshake or
// resize carrying a zero dimension, so a zero is treated as unknown and
// answered with the default rather than passed on and refused.
func (t *TTY) Size() (Size, error) {
	width, height, err := term.GetSize(t.fd)
	if err != nil {
		return Size{}, fmt.Errorf("get terminal size: %w", err)
	}
	size := Size{Cols: uint16(width), Rows: uint16(height)}
	if size.Cols == 0 {
		size.Cols = defaultCols
	}
	if size.Rows == 0 {
		size.Rows = defaultRows
	}
	return size, nil
}

// Resize is the channel of window-size changes to forward to the remote PTY.
func (t *TTY) Resize() <-chan Size {
	return t.resize
}

// emit delivers a size change, coalescing when the consumer is behind: only the
// latest size matters.
func (t *TTY) emit(size Size) {
	select {
	case t.resize <- size:
	default:
		select {
		case <-t.resize:
		default:
		}
		select {
		case t.resize <- size:
		default:
		}
	}
}
