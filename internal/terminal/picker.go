package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrPickCancelled reports that the user dismissed the picker rather than
// choosing. Callers should treat it as "no change", not as a failure.
var ErrPickCancelled = errors.New("selection cancelled")

// PickItem is one row of a picker.
type PickItem struct {
	// Label is the identifying text — a name, the thing being chosen.
	Label string
	// Detail is dimmer trailing context: an ID, a status, an endpoint.
	Detail string
	// Current marks the entry already in effect, so the picker opens on it.
	Current bool
}

// Pick shows an arrow-key list and returns the chosen index.
//
// Typing a number works when a list is short and read carefully, and stops
// working the moment either is untrue: the number has to be found, matched to a
// row and typed correctly, and a mistake is committed the instant it is typed.
// Moving a highlight has none of those steps — what is about to happen is on
// screen until Enter.
//
// Requires a terminal on both sides: the caller is expected to have checked, and
// to offer a non-interactive command instead when there is none.
func Pick(in *os.File, out io.Writer, prompt string, items []PickItem) (int, error) {
	if len(items) == 0 {
		return 0, errors.New("nothing to choose from")
	}
	if !term.IsTerminal(int(in.Fd())) {
		return 0, errors.New("not a terminal")
	}

	cursor := 0
	for index, item := range items {
		if item.Current {
			cursor = index
			break
		}
	}

	// Raw mode, so arrow keys arrive as bytes instead of waiting for a newline.
	state, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return 0, fmt.Errorf("enter raw mode: %w", err)
	}
	restore := func() { _ = term.Restore(int(in.Fd()), state) }
	defer restore()

	fmt.Fprintf(out, "%s\r\n", prompt)
	fmt.Fprint(out, "\x1b[?25l")       // hide the cursor; only the highlight should move
	defer fmt.Fprint(out, "\x1b[?25h") // and always put it back, including on error
	render(out, items, cursor, false)

	buf := make([]byte, 3)
	for {
		n, err := in.Read(buf)
		if err != nil {
			clear(out, len(items))
			return 0, fmt.Errorf("read key: %w", err)
		}

		switch {
		case n == 1 && (buf[0] == '\r' || buf[0] == '\n'):
			clear(out, len(items))
			render(out, items, cursor, true)
			return cursor, nil

		// Ctrl-C, Esc and q all mean "leave things as they are".
		case n == 1 && (buf[0] == 3 || buf[0] == 27 || buf[0] == 'q'):
			clear(out, len(items))
			return 0, ErrPickCancelled

		case n == 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A', // up
			n == 1 && buf[0] == 'k':
			if cursor > 0 {
				cursor--
			}
		case n == 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B', // down
			n == 1 && buf[0] == 'j':
			if cursor < len(items)-1 {
				cursor++
			}
		default:
			continue
		}

		clear(out, len(items))
		render(out, items, cursor, false)
	}
}

// render draws the list and leaves the cursor on the line after it. In raw mode
// a bare \n only moves down, so every line ends \r\n.
func render(out io.Writer, items []PickItem, cursor int, chosen bool) {
	width := 0
	for _, item := range items {
		if len(item.Label) > width {
			width = len(item.Label)
		}
	}

	if chosen {
		item := items[cursor]
		fmt.Fprintf(out, "  %s%s\r\n", item.Label, detailSuffix(item, 0))
		return
	}

	for index, item := range items {
		marker, style, reset := "  ", "", ""
		if index == cursor {
			marker, style, reset = "❯ ", "\x1b[1m", "\x1b[0m"
		}
		fmt.Fprintf(out, "%s%s%s%s%s\r\n", marker, style, item.Label, reset, detailSuffix(item, width-len(item.Label)))
	}
}

func detailSuffix(item PickItem, pad int) string {
	if item.Detail == "" {
		return ""
	}
	// Dim, so the eye lands on the name rather than the metadata beside it.
	return strings.Repeat(" ", pad) + "  \x1b[2m" + item.Detail + "\x1b[0m"
}

// clear removes the rendered list so the next frame overwrites it rather than
// scrolling a new copy into view.
func clear(out io.Writer, lines int) {
	for i := 0; i < lines; i++ {
		fmt.Fprint(out, "\x1b[A\x1b[2K\r")
	}
}
