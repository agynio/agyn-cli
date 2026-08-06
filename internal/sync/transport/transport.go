// Package transport carries the sync endpoint protocol over the Terminal Proxy
// WebSocket, presenting it to the client as a plain reader and writer.
//
// A non-TTY session tags each output frame with the stream it came from, so the
// protocol stream can be separated from diagnostics. A TTY session cannot carry
// this protocol at all: a PTY merges the two at the kernel and translates line
// endings.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// Stream identifiers the proxy prefixes to non-TTY output frames.
const (
	streamStdout byte = 0
	streamStderr byte = 1
)

// readLimit caps a single inbound frame. Protocol frames are chunked well below
// this; it only guards against a malformed peer.
const readLimit = 32 << 20

// Conn is one endpoint session. Read yields protocol bytes; diagnostics are
// collected separately and surfaced when something fails.
type Conn struct {
	ws *websocket.Conn

	readMu    sync.Mutex
	pending   []byte
	readErr   error
	diagMu    sync.Mutex
	diagnosti strings.Builder

	closeOnce sync.Once
}

// Dial opens the session. The ticket is single-use and short-lived, which is
// why it travels as a query parameter rather than a bearer token.
func Dial(ctx context.Context, websocketURL, ticket string, httpClient *http.Client) (*Conn, error) {
	endpoint, err := ticketURL(websocketURL, ticket)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	ws, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		return nil, fmt.Errorf("connect to terminal proxy: %w", err)
	}
	ws.SetReadLimit(readLimit)
	return &Conn{ws: ws}, nil
}

// Read returns protocol bytes only. Frames tagged as stderr are diverted to the
// diagnostics buffer: one byte of them in the protocol stream would corrupt it
// beyond recovery.
func (c *Conn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.pending) == 0 {
		if c.readErr != nil {
			return 0, c.readErr
		}
		if err := c.fill(); err != nil {
			c.readErr = err
			if len(c.pending) == 0 {
				return 0, err
			}
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// fill reads one frame, routing it by its stream tag.
func (c *Conn) fill() error {
	messageType, data, err := c.ws.Read(context.Background())
	if err != nil {
		if isBenignClose(err) {
			return io.EOF
		}
		return err
	}
	switch messageType {
	case websocket.MessageBinary:
		if len(data) == 0 {
			return nil
		}
		switch data[0] {
		case streamStdout:
			c.pending = append(c.pending, data[1:]...)
		case streamStderr:
			c.diagMu.Lock()
			c.diagnosti.Write(data[1:])
			c.diagMu.Unlock()
		default:
			return fmt.Errorf("unknown stream tag %d — is this a TTY session?", data[0])
		}
	case websocket.MessageText:
		// The proxy's exit control frame. The stream is over; whatever the
		// endpoint wrote to stderr is the useful part.
		return io.EOF
	}
	return nil
}

func (c *Conn) Write(p []byte) (int, error) {
	if err := c.ws.Write(context.Background(), websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Diagnostics is whatever the endpoint wrote to stderr, surfaced by the CLI
// when a session fails.
func (c *Conn) Diagnostics() string {
	c.diagMu.Lock()
	defer c.diagMu.Unlock()
	return strings.TrimSpace(c.diagnosti.String())
}

func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.ws.Close(websocket.StatusNormalClosure, "sync session ended") })
	return err
}

// ticketURL puts the ticket on the query string and normalizes http(s) to
// ws(s), so callers can pass either form.
func ticketURL(raw, ticket string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("terminal proxy URL is empty")
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
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return true
	}
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}
