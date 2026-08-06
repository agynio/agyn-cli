package transport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agynio/agyn-cli/internal/sync/transport"
	"github.com/coder/websocket"
)

// proxyStub speaks the frame shape the Terminal Proxy uses for a non-TTY
// session: binary frames prefixed with the stream they came from.
func proxyStub(t *testing.T, script func(ctx context.Context, conn *websocket.Conn)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") == "" {
			http.Error(w, "ticket required", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		script(ctx, conn)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// The whole reason the proxy tags non-TTY output: a diagnostic written by
// something in the container must not land inside the protocol stream.
func TestStderrIsDivertedFromTheProtocolStream(t *testing.T) {
	url := proxyStub(t, func(ctx context.Context, conn *websocket.Conn) {
		_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{0}, []byte("proto-")...))
		_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{1}, []byte("a warning from a library")...))
		_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{0}, []byte("frames")...))
		_ = conn.Close(websocket.StatusNormalClosure, "done")
	})

	conn, err := transport.Dial(context.Background(), url, "ticket", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	got := &strings.Builder{}
	buf := make([]byte, 64)
	for {
		n, err := conn.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if got.String() != "proto-frames" {
		t.Fatalf("protocol stream was contaminated: %q", got.String())
	}
	if diagnostics := conn.Diagnostics(); diagnostics != "a warning from a library" {
		t.Fatalf("diagnostics not captured, got %q", diagnostics)
	}
}

// A TTY session sends untagged bytes. Reading one as a sync session would
// silently consume the first byte of every frame, so it is refused instead.
func TestUntaggedFramesAreRefused(t *testing.T) {
	url := proxyStub(t, func(ctx context.Context, conn *websocket.Conn) {
		// A shell banner: first byte is 'W', which is neither stream tag.
		_ = conn.Write(ctx, websocket.MessageBinary, []byte("Welcome to the sandbox\r\n"))
		<-ctx.Done()
	})

	conn, err := transport.Dial(context.Background(), url, "ticket", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_, err = conn.Read(make([]byte, 64))
	if err == nil {
		t.Fatal("expected an untagged frame to be refused")
	}
	if !strings.Contains(err.Error(), "unknown stream tag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExitFrameEndsTheStream(t *testing.T) {
	url := proxyStub(t, func(ctx context.Context, conn *websocket.Conn) {
		_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{0}, []byte("tail")...))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"exit","code":1,"reason":"error"}`))
		<-ctx.Done()
	})

	conn, err := transport.Dial(context.Background(), url, "ticket", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	if string(buf[:n]) != "tail" {
		t.Fatalf("lost buffered protocol bytes: %q", buf[:n])
	}
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected the exit frame to end the stream")
	}
}
