// Package wire carries the sync endpoint protocol over one exec stream: length-
// prefixed protobuf frames, request/response, one request in flight.
//
// One request in flight governs requests, not frames. A request that carries a
// body is followed by chunk frames terminated by an eof chunk, which is what
// lets content stream rather than be buffered whole — the endpoint shares the
// container's cgroup and cannot afford to hold a file in memory.
package wire

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// ChunkSize is the body chunk. Matches the file upload chunk the CLI
	// already uses.
	ChunkSize = 64 << 10

	// MaxFrameSize bounds a single frame. Bodies are chunked, so the only
	// frames near this are scan responses on very wide trees.
	MaxFrameSize = 16 << 20
)

// ErrBodyTooLarge reports a body that exceeded the size its request declared.
// The endpoint stops reading rather than filling the workspace volume.
var ErrBodyTooLarge = errors.New("body exceeds declared size")

// Conn is one end of the protocol. Reads are single-threaded by the
// request/response discipline; writes take a mutex because a body is written as
// many frames and must not interleave with anything else.
type Conn struct {
	r      *bufio.Reader
	w      io.Writer
	writeM sync.Mutex
	hdr    [4]byte
}

func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{r: bufio.NewReaderSize(r, ChunkSize+4096), w: w}
}

func (c *Conn) WriteFrame(frame *syncv1.Frame) error {
	payload, err := proto.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("frame of %d bytes exceeds the %d limit", len(payload), MaxFrameSize)
	}
	c.writeM.Lock()
	defer c.writeM.Unlock()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := c.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = c.w.Write(payload)
	return err
}

func (c *Conn) ReadFrame() (*syncv1.Frame, error) {
	if _, err := io.ReadFull(c.r, c.hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(c.hdr[:])
	if size > MaxFrameSize {
		return nil, fmt.Errorf("frame of %d bytes exceeds the %d limit", size, MaxFrameSize)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return nil, err
	}
	frame := &syncv1.Frame{}
	if err := proto.Unmarshal(payload, frame); err != nil {
		return nil, fmt.Errorf("unmarshal frame: %w", err)
	}
	return frame, nil
}

func (c *Conn) WriteRequest(request *syncv1.Request) error {
	return c.WriteFrame(&syncv1.Frame{Payload: &syncv1.Frame_Request{Request: request}})
}

func (c *Conn) WriteResponse(response *syncv1.Response) error {
	return c.WriteFrame(&syncv1.Frame{Payload: &syncv1.Frame_Response{Response: response}})
}

// WriteBody streams src as chunk frames and terminates with an eof chunk. It
// stops at limit bytes so a source that grew since it was declared cannot run
// unbounded; pass a negative limit for no bound.
func (c *Conn) WriteBody(src io.Reader, limit int64) error {
	buf := make([]byte, ChunkSize)
	var written int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			written += int64(n)
			if limit >= 0 && written > limit {
				return ErrBodyTooLarge
			}
			if writeErr := c.WriteFrame(&syncv1.Frame{
				Payload: &syncv1.Frame_Chunk{Chunk: &syncv1.Chunk{Data: buf[:n]}},
			}); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	return c.WriteFrame(&syncv1.Frame{Payload: &syncv1.Frame_Chunk{Chunk: &syncv1.Chunk{Eof: true}}})
}

// ReadBody consumes chunk frames into dst until the eof chunk. A frame that is
// not a chunk is a protocol violation: the peer cannot start something else
// while a body is in flight.
func (c *Conn) ReadBody(dst io.Writer, limit int64) error {
	var read int64
	for {
		frame, err := c.ReadFrame()
		if err != nil {
			return err
		}
		chunk := frame.GetChunk()
		if chunk == nil {
			return fmt.Errorf("expected a chunk frame mid-body, got %T", frame.GetPayload())
		}
		if chunk.GetEof() {
			return nil
		}
		read += int64(len(chunk.GetData()))
		if limit >= 0 && read > limit {
			return ErrBodyTooLarge
		}
		if _, err := dst.Write(chunk.GetData()); err != nil {
			return err
		}
	}
}

// DiscardBody drains a body the caller has decided not to keep. The stream is a
// single pipe with no way to cancel, so an unwanted body must still be read to
// its end before the next request can be sent.
func (c *Conn) DiscardBody() error {
	return c.ReadBody(io.Discard, -1)
}
