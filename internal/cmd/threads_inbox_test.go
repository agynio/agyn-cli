package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
)

type recordingAgentsGateway struct {
	gatewayv1connect.UnimplementedAgentsGatewayHandler
	items   []*agentsv1.InboxItem
	ackReq  *agentsv1.AckInboxItemsRequest
	fetched *agentsv1.GetUnackedInboxItemsRequest
}

func (s *recordingAgentsGateway) GetUnackedInboxItems(ctx context.Context, req *connect.Request[agentsv1.GetUnackedInboxItemsRequest]) (*connect.Response[agentsv1.GetUnackedInboxItemsResponse], error) {
	s.fetched = req.Msg
	return connect.NewResponse(&agentsv1.GetUnackedInboxItemsResponse{Items: s.items}), nil
}

func (s *recordingAgentsGateway) AckInboxItems(ctx context.Context, req *connect.Request[agentsv1.AckInboxItemsRequest]) (*connect.Response[agentsv1.AckInboxItemsResponse], error) {
	s.ackReq = req.Msg
	return connect.NewResponse(&agentsv1.AckInboxItemsResponse{AckedCount: int32(len(req.Msg.GetItemIds()))}), nil
}

func newInboxSurfaceForTest(t *testing.T, service *recordingAgentsGateway) *inboxSurface {
	t.Helper()
	path, handler := gatewayv1connect.NewAgentsGatewayHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &inboxSurface{
		client:     gatewayv1connect.NewAgentsGatewayClient(server.Client(), server.URL),
		instanceID: "instance-1",
		itemIDs:    map[string]string{},
	}
}

func threadItem(itemID, messageID, threadID string) *agentsv1.InboxItem {
	return &agentsv1.InboxItem{
		Id:         itemID,
		ThreadId:   &threadID,
		MessageId:  &messageID,
		SourceKind: agentsv1.InboxItemSourceKind_INBOX_ITEM_SOURCE_KIND_THREAD,
		SenderId:   "sender-1",
		Body:       "hello",
		AcceptedAt: timestamppb.Now(),
	}
}

func TestUnreadSurfaceFollowsCallerKind(t *testing.T) {
	t.Setenv(agynIdentityIDEnv, "")
	if room := callerRoom(); room != threadParticipantSelfRoom {
		t.Fatalf("user caller waits on %q, want %q", room, threadParticipantSelfRoom)
	}
	t.Setenv(agynIdentityIDEnv, "instance-1")
	if room := callerRoom(); room != instanceInboxSelfRoom {
		t.Fatalf("agent caller waits on %q, want %q", room, instanceInboxSelfRoom)
	}
}

func TestInboxSurfaceFetchesThreadItems(t *testing.T) {
	direct := &agentsv1.InboxItem{
		Id:         "item-direct",
		SourceKind: agentsv1.InboxItemSourceKind_INBOX_ITEM_SOURCE_KIND_DIRECT,
		SenderId:   "sender-1",
		Body:       "direct",
		AcceptedAt: timestamppb.Now(),
	}
	service := &recordingAgentsGateway{items: []*agentsv1.InboxItem{
		threadItem("item-1", "message-1", "thread-1"),
		threadItem("item-2", "message-2", "thread-other"),
		direct,
	}}
	surface := newInboxSurfaceForTest(t, service)

	messages, err := surface.fetch(context.Background(), []threadTarget{{ID: "thread-1"}})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want only the one on thread-1", len(messages))
	}
	if messages[0].GetId() != "message-1" || messages[0].GetThreadId() != "thread-1" {
		t.Fatalf("got message %q on thread %q, want message-1 on thread-1", messages[0].GetId(), messages[0].GetThreadId())
	}
	if messages[0].GetBody() != "hello" || messages[0].GetSenderId() != "sender-1" {
		t.Fatalf("message lost its body or sender: %+v", messages[0])
	}
	if service.fetched.GetAgentInstanceId() != "instance-1" {
		t.Fatalf("read as %q, want instance-1", service.fetched.GetAgentInstanceId())
	}
}

// The ack addresses the item, not the message inside it. Sending message ids
// here acks nothing and the caller is handed the same message forever.
func TestInboxSurfaceAcksItemIDs(t *testing.T) {
	service := &recordingAgentsGateway{items: []*agentsv1.InboxItem{threadItem("item-1", "message-1", "thread-1")}}
	surface := newInboxSurfaceForTest(t, service)

	ctx := context.Background()
	if _, err := surface.fetch(ctx, nil); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := surface.ack(ctx, []messageView{{ID: "message-1"}, {ID: "message-1"}}); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if service.ackReq == nil {
		t.Fatal("ack never reached the inbox")
	}
	got := service.ackReq.GetItemIds()
	if len(got) != 1 || got[0] != "item-1" {
		t.Fatalf("acked %v, want [item-1]", got)
	}
}
