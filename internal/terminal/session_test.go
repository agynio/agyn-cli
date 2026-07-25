package terminal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestTicketURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "https becomes wss", raw: "https://terminal.example.com/terminal", want: "wss://terminal.example.com/terminal?ticket=abc"},
		{name: "http becomes ws", raw: "http://terminal.example.com/terminal", want: "ws://terminal.example.com/terminal?ticket=abc"},
		{name: "wss preserved", raw: "wss://terminal.example.com/terminal", want: "wss://terminal.example.com/terminal?ticket=abc"},
		{name: "existing query preserved", raw: "wss://terminal.example.com/terminal?x=1", want: "wss://terminal.example.com/terminal?ticket=abc&x=1"},
		{name: "empty rejected", raw: "", wantErr: true},
		{name: "bad scheme rejected", raw: "ftp://terminal.example.com", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ticketURL(test.raw, "abc")
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", test.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

// fakeProxy stands in for the Terminal Proxy: it records what the client sends
// and drives the client through a full session.
type fakeProxy struct {
	handshake  chan controlMessage
	resizes    chan controlMessage
	stdin      chan []byte
	serverSend chan []byte
	exit       controlMessage
}

func newFakeProxy(exit controlMessage) *fakeProxy {
	return &fakeProxy{
		handshake:  make(chan controlMessage, 1),
		resizes:    make(chan controlMessage, 8),
		stdin:      make(chan []byte, 8),
		serverSend: make(chan []byte, 8),
		exit:       exit,
	}
}

func (f *fakeProxy) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") == "" {
			t.Errorf("ticket missing from WebSocket URL")
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		first := true
		go func() {
			for {
				msgType, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				switch msgType {
				case websocket.MessageText:
					var control controlMessage
					if err := json.Unmarshal(data, &control); err != nil {
						continue
					}
					if first {
						first = false
						f.handshake <- control
						continue
					}
					f.resizes <- control
				case websocket.MessageBinary:
					f.stdin <- data
				}
			}
		}()

		for payload := range f.serverSend {
			if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
				return
			}
		}
		payload, _ := json.Marshal(f.exit)
		_ = conn.Write(ctx, websocket.MessageText, payload)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
}

func TestAttachBridgesSessionAndReturnsExitCode(t *testing.T) {
	proxy := newFakeProxy(controlMessage{Type: controlTypeExit, Code: 42, Reason: string(ReasonCompleted)})
	server := proxy.server(t)
	defer server.Close()

	stdinReader, stdinWriter := io.Pipe()
	var stdout strings.Builder
	resize := make(chan Size, 1)

	done := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := Attach(context.Background(), Options{
			URL:         server.URL,
			Ticket:      "ticket-value",
			InitialSize: Size{Cols: 120, Rows: 40},
			Resize:      resize,
			Stdin:       stdinReader,
			Stdout:      &stdout,
			HTTPClient:  server.Client(),
		})
		errCh <- err
		done <- result
	}()

	// The handshake must carry the initial size and nothing else.
	select {
	case handshake := <-proxy.handshake:
		if handshake.Cols != 120 || handshake.Rows != 40 {
			t.Fatalf("handshake size = %dx%d, want 120x40", handshake.Cols, handshake.Rows)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handshake")
	}

	// Keystrokes reach the proxy as binary frames, byte for byte.
	if _, err := stdinWriter.Write([]byte("echo hi\r")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	select {
	case got := <-proxy.stdin:
		if string(got) != "echo hi\r" {
			t.Fatalf("stdin = %q, want %q", got, "echo hi\r")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stdin frame")
	}

	// A window change becomes a resize control message.
	resize <- Size{Cols: 80, Rows: 24}
	select {
	case got := <-proxy.resizes:
		if got.Type != controlTypeResize || got.Cols != 80 || got.Rows != 24 {
			t.Fatalf("resize = %+v, want resize 80x24", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for resize")
	}

	// PTY output is written to the local terminal untouched.
	proxy.serverSend <- []byte("\x1b[32mhi\x1b[0m")
	close(proxy.serverSend)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Attach returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Attach to return")
	}
	result := <-done

	if result.Code != 42 {
		t.Fatalf("exit code = %d, want 42", result.Code)
	}
	if result.Reason != ReasonCompleted {
		t.Fatalf("reason = %q, want %q", result.Reason, ReasonCompleted)
	}
	if !strings.Contains(stdout.String(), "\x1b[32mhi\x1b[0m") {
		t.Fatalf("stdout = %q, want the escape sequence passed through verbatim", stdout.String())
	}
	_ = stdinWriter.Close()
}

func TestAttachReportsCancelledSession(t *testing.T) {
	proxy := newFakeProxy(controlMessage{Type: controlTypeExit, Code: 1, Reason: string(ReasonCancelled)})
	server := proxy.server(t)
	defer server.Close()

	stdinReader, stdinWriter := io.Pipe()
	defer stdinWriter.Close()
	close(proxy.serverSend)

	result, err := Attach(context.Background(), Options{
		URL:         server.URL,
		Ticket:      "ticket-value",
		InitialSize: Size{Cols: 80, Rows: 24},
		Stdin:       stdinReader,
		Stdout:      io.Discard,
		HTTPClient:  server.Client(),
	})
	if err != nil {
		t.Fatalf("Attach returned error: %v", err)
	}
	if result.Reason != ReasonCancelled {
		t.Fatalf("reason = %q, want %q", result.Reason, ReasonCancelled)
	}
}
