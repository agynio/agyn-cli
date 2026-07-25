// Package terminal attaches the local TTY to a remote container PTY through the
// Terminal Proxy. The wire protocol is defined in
// architecture/architecture/terminal-proxy.md: binary frames carry raw PTY
// bytes in both directions, text frames carry JSON control messages.
package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	// handshakeType is the first message the client sends. It carries only the
	// initial terminal size; the command was bound into the ticket at issuance
	// so a client cannot escalate beyond what it was authorized for.
	handshakeType = "resize"

	controlTypeResize = "resize"
	controlTypeExit   = "exit"

	// readLimit caps a single inbound frame. PTY output frames are small; this
	// only guards against a malformed peer.
	readLimit = 1 << 20
)

// ExitReason distinguishes a normal shell exit from a session torn down by the
// platform, so the CLI can print a notice for the latter.
type ExitReason string

const (
	ReasonCompleted ExitReason = "completed"
	ReasonCancelled ExitReason = "cancelled"
	ReasonError     ExitReason = "error"
)

// Result reports how the remote side ended.
type Result struct {
	Code   int
	Reason ExitReason
}

type controlMessage struct {
	Type   string `json:"type"`
	Cols   uint16 `json:"cols,omitempty"`
	Rows   uint16 `json:"rows,omitempty"`
	Code   int    `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Size is a terminal window size in character cells.
type Size struct {
	Cols uint16
	Rows uint16
}

// Options configures a single attach.
type Options struct {
	// URL is the Terminal Proxy WebSocket endpoint from CreateTerminalSession.
	URL string
	// Ticket authenticates the connection. It is single-use and short-lived,
	// which is why it travels as a query parameter rather than a bearer token.
	Ticket string
	// InitialSize seeds the remote PTY before the shell draws anything.
	InitialSize Size
	// Resize delivers subsequent size changes (SIGWINCH on Unix). May be nil.
	Resize <-chan Size
	// Stdin/Stdout carry raw bytes. Stdin should already be in raw mode.
	Stdin  io.Reader
	Stdout io.Writer
	// HTTPClient dials the WebSocket. Nil uses http.DefaultClient.
	HTTPClient *http.Client
}

// Attach runs the session until the remote side exits, the context is
// cancelled, or the connection fails. It returns the remote exit result.
func Attach(ctx context.Context, opts Options) (Result, error) {
	endpoint, err := ticketURL(opts.URL, opts.Ticket)
	if err != nil {
		return Result{}, err
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		return Result{}, fmt.Errorf("connect to terminal proxy: %w", err)
	}
	conn.SetReadLimit(readLimit)
	// The proxy sends the exit frame before closing; anything still open when we
	// return is an abnormal teardown.
	defer conn.CloseNow()

	if err := writeControl(ctx, conn, controlMessage{
		Type: handshakeType,
		Cols: opts.InitialSize.Cols,
		Rows: opts.InitialSize.Rows,
	}); err != nil {
		return Result{}, fmt.Errorf("send handshake: %w", err)
	}

	// Writers are serialized through a single goroutine: the WebSocket allows
	// only one concurrent writer, and input and resize both produce frames.
	writeErr := make(chan error, 1)
	go func() { writeErr <- pumpOutbound(ctx, conn, opts) }()

	result, readErr := pumpInbound(ctx, conn, opts.Stdout)
	cancel()

	if readErr != nil {
		return result, readErr
	}
	select {
	case err := <-writeErr:
		// A write failure after a clean exit frame is just the teardown race.
		if err != nil && !isBenignClose(err) && result.Reason == "" {
			return result, err
		}
	default:
	}
	return result, nil
}

// pumpOutbound forwards stdin bytes and resize events to the proxy.
func pumpOutbound(ctx context.Context, conn *websocket.Conn, opts Options) error {
	frames := make(chan []byte, 16)
	readDone := make(chan error, 1)

	go func() {
		defer close(frames)
		buf := make([]byte, 4096)
		for {
			n, err := opts.Stdin.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case frames <- chunk:
				case <-ctx.Done():
					readDone <- ctx.Err()
					return
				}
			}
			if err != nil {
				readDone <- err
				return
			}
		}
	}()

	resize := opts.Resize
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case size, ok := <-resize:
			if !ok {
				resize = nil
				continue
			}
			if err := writeControl(ctx, conn, controlMessage{
				Type: controlTypeResize,
				Cols: size.Cols,
				Rows: size.Rows,
			}); err != nil {
				return err
			}
		case chunk, ok := <-frames:
			if !ok {
				// Stdin closed: stop writing but let the remote shell decide
				// when the session ends.
				select {
				case err := <-readDone:
					if err != nil && !errors.Is(err, io.EOF) {
						return err
					}
				default:
				}
				<-ctx.Done()
				return ctx.Err()
			}
			if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return err
			}
		}
	}
}

// pumpInbound writes PTY output to the local terminal and returns once the
// proxy reports an exit.
func pumpInbound(ctx context.Context, conn *websocket.Conn, stdout io.Writer) (Result, error) {
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			if isBenignClose(err) {
				// Closed without an exit frame — treat as an abnormal end so the
				// caller prints a notice rather than implying a clean shell exit.
				return Result{Code: 1, Reason: ReasonError}, nil
			}
			return Result{Code: 1, Reason: ReasonError}, err
		}

		switch msgType {
		case websocket.MessageBinary:
			if _, err := stdout.Write(data); err != nil {
				return Result{Code: 1, Reason: ReasonError}, err
			}
		case websocket.MessageText:
			var control controlMessage
			if err := json.Unmarshal(data, &control); err != nil {
				// Unknown control payloads are ignored rather than fatal: the
				// data path must stay transparent.
				continue
			}
			if control.Type == controlTypeExit {
				return Result{Code: control.Code, Reason: ExitReason(control.Reason)}, nil
			}
		}
	}
}

func writeControl(ctx context.Context, conn *websocket.Conn, message controlMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

// ticketURL puts the ticket on the query string and normalizes http(s) to ws(s)
// so callers can pass either form.
func ticketURL(raw, ticket string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("terminal proxy URL is empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse terminal proxy URL: %w", err)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported terminal proxy scheme %q", parsed.Scheme)
	}
	query := parsed.Query()
	query.Set("ticket", ticket)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func isBenignClose(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return true
	}
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}

// DialTimeout bounds the initial WebSocket handshake.
const DialTimeout = 30 * time.Second
