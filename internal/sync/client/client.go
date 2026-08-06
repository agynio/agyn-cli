// Package client is the controller side of the sync endpoint protocol. It
// decides every action; the endpoint executes and computes nothing.
package client

import (
	"fmt"
	"io"

	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	"github.com/agynio/agyn-cli/internal/sync/endpoint"
	"github.com/agynio/agyn-cli/internal/sync/wire"
)

// Error is a failure the endpoint reported for a request, as opposed to a
// transport failure. The code is what the caller branches on — a root outside
// the workspace is a user error, an internal failure is not.
type Error struct {
	Code    syncv1.ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

// Client speaks to one endpoint over one stream. The protocol is strictly one
// request in flight, so a Client is not safe for concurrent use.
type Client struct {
	conn *wire.Conn
}

func New(in io.Reader, out io.Writer) *Client {
	return &Client{conn: wire.NewConn(in, out)}
}

func (c *Client) roundTrip(request *syncv1.Request) (*syncv1.Response, error) {
	if err := c.conn.WriteRequest(request); err != nil {
		return nil, err
	}
	return c.readResponse()
}

func (c *Client) readResponse() (*syncv1.Response, error) {
	frame, err := c.conn.ReadFrame()
	if err != nil {
		return nil, err
	}
	response := frame.GetResponse()
	if response == nil {
		return nil, fmt.Errorf("expected a response frame, got %T", frame.GetPayload())
	}
	if failure := response.GetError(); failure != nil {
		return nil, &Error{Code: failure.GetCode(), Message: failure.GetMessage()}
	}
	return response, nil
}

// Handshake opens the session. markerMode decides whether the endpoint may
// create a root marker: sync does, cp never does.
func (c *Client) Handshake(markerMode syncv1.MarkerMode, expectedSessionID string) (*syncv1.HandshakeResponse, error) {
	response, err := c.roundTrip(&syncv1.Request{Request: &syncv1.Request_Handshake{
		Handshake: &syncv1.HandshakeRequest{
			VersionMin:        endpoint.MinProtocolVersion,
			VersionMax:        endpoint.ProtocolVersion,
			MarkerMode:        markerMode,
			ExpectedSessionId: expectedSessionID,
		},
	}})
	if err != nil {
		return nil, err
	}
	handshake := response.GetHandshake()
	if handshake == nil {
		return nil, fmt.Errorf("expected a handshake response, got %T", response.GetResponse())
	}
	return handshake, nil
}

func (c *Client) Scan(cache []*syncv1.CachedDigest) (*syncv1.ScanResponse, error) {
	response, err := c.roundTrip(&syncv1.Request{Request: &syncv1.Request_Scan{
		Scan: &syncv1.ScanRequest{Cache: cache},
	}})
	if err != nil {
		return nil, err
	}
	scan := response.GetScan()
	if scan == nil {
		return nil, fmt.Errorf("expected a scan response, got %T", response.GetResponse())
	}
	return scan, nil
}

// StageQuery reports which of the digests the endpoint does not already hold.
// Content it has from an earlier cycle is never sent twice.
func (c *Client) StageQuery(digests []string) (*syncv1.StageQueryResponse, error) {
	response, err := c.roundTrip(&syncv1.Request{Request: &syncv1.Request_StageQuery{
		StageQuery: &syncv1.StageQueryRequest{Digests: digests},
	}})
	if err != nil {
		return nil, err
	}
	query := response.GetStageQuery()
	if query == nil {
		return nil, fmt.Errorf("expected a stage query response, got %T", response.GetResponse())
	}
	return query, nil
}

// StagePut streams one file into the endpoint's staging directory and verifies
// the digest it computed over what actually arrived.
func (c *Client) StagePut(digest string, size int64, src io.Reader) error {
	if err := c.conn.WriteRequest(&syncv1.Request{Request: &syncv1.Request_StagePut{
		StagePut: &syncv1.StagePutRequest{Digest: digest, Size: size},
	}}); err != nil {
		return err
	}
	if err := c.conn.WriteBody(src, size); err != nil {
		return err
	}
	response, err := c.readResponse()
	if err != nil {
		return err
	}
	put := response.GetStagePut()
	if put == nil {
		return fmt.Errorf("expected a stage put response, got %T", response.GetResponse())
	}
	if put.GetDigest() != digest {
		return fmt.Errorf("content for %s arrived as %s", digest, put.GetDigest())
	}
	return nil
}

func (c *Client) Transition(changes []*syncv1.Change) ([]*syncv1.Result, error) {
	response, err := c.roundTrip(&syncv1.Request{Request: &syncv1.Request_Transition{
		Transition: &syncv1.TransitionRequest{Changes: changes},
	}})
	if err != nil {
		return nil, err
	}
	transition := response.GetTransition()
	if transition == nil {
		return nil, fmt.Errorf("expected a transition response, got %T", response.GetResponse())
	}
	return transition.GetResults(), nil
}

// Supply streams one file out of the endpoint into dst. expectedDigest is what
// the controller believes the scan found; the endpoint refuses rather than
// sending different content under that name.
func (c *Client) Supply(path, expectedDigest string, dst io.Writer) (*syncv1.SupplyResponse, error) {
	if err := c.conn.WriteRequest(&syncv1.Request{Request: &syncv1.Request_Supply{
		Supply: &syncv1.SupplyRequest{Path: path, ExpectedDigest: expectedDigest},
	}}); err != nil {
		return nil, err
	}
	response, err := c.readResponse()
	if err != nil {
		return nil, err
	}
	supply := response.GetSupply()
	if supply == nil {
		return nil, fmt.Errorf("expected a supply response, got %T", response.GetResponse())
	}
	if err := c.conn.ReadBody(dst, supply.GetSize()); err != nil {
		return nil, err
	}
	return supply, nil
}
