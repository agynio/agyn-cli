package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	notificationsv1 "github.com/agynio/agyn-cli/gen/agynio/api/notifications/v1"
	threadsv1 "github.com/agynio/agyn-cli/gen/agynio/api/threads/v1"
	"github.com/agynio/agyn-cli/internal/gateway"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestResolveThreadTargets(t *testing.T) {
	refs := map[string]string{"research": "thread-1"}
	targets, err := resolveThreadTargets([]string{"research", "thread-1"}, refs)
	if err != nil {
		t.Fatalf("resolve targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].ID != "thread-1" || targets[0].Ref != "research" {
		t.Fatalf("unexpected target: %#v", targets[0])
	}

	t.Setenv(threadIDEnv, "env-ref")
	refs = map[string]string{"env-ref": "thread-env"}
	targets, err = resolveThreadTargets(nil, refs)
	if err != nil {
		t.Fatalf("resolve env target: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "thread-env" || targets[0].Ref != "env-ref" {
		t.Fatalf("unexpected env target: %#v", targets)
	}

	t.Setenv(threadIDEnv, "")
	if _, err := resolveThreadTargets(nil, refs); err == nil {
		t.Fatalf("expected error when thread is missing")
	}
	if _, err := resolveThreadTargets([]string{" "}, refs); err == nil {
		t.Fatalf("expected error for empty thread ref")
	}
}

func TestSplitParticipants(t *testing.T) {
	ids, nicknames, err := splitParticipants([]string{"@alice", "user-1", "@bob", "user-1"})
	if err != nil {
		t.Fatalf("split participants: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"user-1"}) {
		t.Fatalf("unexpected ids: %#v", ids)
	}
	if !reflect.DeepEqual(nicknames, []string{"@alice", "@bob"}) {
		t.Fatalf("unexpected nicknames: %#v", nicknames)
	}
	if _, _, err := splitParticipants([]string{""}); err == nil {
		t.Fatalf("expected error for empty participant")
	}
}

func TestRunThreadsCreateAddsPassiveCreatorByDefault(t *testing.T) {
	t.Setenv(agentIDEnv, "agent-1")
	t.Setenv("HOME", t.TempDir())

	recorder := &threadsGatewayRecorder{}
	runContext := newThreadsGatewayRunContext(t, recorder)
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetContext(withRunContext(context.Background(), runContext))

	if err := runThreadsCreate(cmd, &threadsCreateArgs{add: []string{"agent-2", "@alice"}, passive: true}); err != nil {
		t.Fatalf("run threads create: %v", err)
	}

	if recorder.createRequest == nil {
		t.Fatal("expected create request")
	}
	if !reflect.DeepEqual(recorder.createRequest.ParticipantIds, []string{"agent-2"}) {
		t.Fatalf("unexpected participant ids: %#v", recorder.createRequest.ParticipantIds)
	}

	creatorReq := findAddParticipantByID(recorder.addRequests, "agent-1")
	if creatorReq == nil {
		t.Fatal("expected creator add participant request")
	}
	if creatorReq.Passive != true {
		t.Fatalf("expected creator passive=true, got %v", creatorReq.Passive)
	}

	nicknameReq := findAddParticipantByNickname(recorder.addRequests, "@alice")
	if nicknameReq == nil {
		t.Fatal("expected nickname add participant request")
	}
	if nicknameReq.Passive {
		t.Fatal("expected nickname participant passive=false")
	}
}

func TestRunThreadsCreateAllowsActiveCreator(t *testing.T) {
	t.Setenv(agentIDEnv, "agent-1")
	t.Setenv("HOME", t.TempDir())

	recorder := &threadsGatewayRecorder{}
	runContext := newThreadsGatewayRunContext(t, recorder)
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetContext(withRunContext(context.Background(), runContext))

	if err := runThreadsCreate(cmd, &threadsCreateArgs{add: []string{"agent-2"}, passive: false}); err != nil {
		t.Fatalf("run threads create: %v", err)
	}

	creatorReq := findAddParticipantByID(recorder.addRequests, "agent-1")
	if creatorReq == nil {
		t.Fatal("expected creator add participant request")
	}
	if creatorReq.Passive {
		t.Fatal("expected creator passive=false")
	}
}

func TestParticipantIdentifier(t *testing.T) {
	identifier, err := participantIdentifier(" @agent ")
	if err != nil {
		t.Fatalf("participant identifier: %v", err)
	}
	nickname, ok := identifier.GetIdentifier().(*threadsv1.ParticipantIdentifier_ParticipantNickname)
	if !ok {
		t.Fatalf("expected nickname identifier, got %#v", identifier.GetIdentifier())
	}
	if nickname.ParticipantNickname != "@agent" {
		t.Fatalf("unexpected nickname: %s", nickname.ParticipantNickname)
	}

	identifier, err = participantIdentifier(" agent-1 ")
	if err != nil {
		t.Fatalf("participant identifier: %v", err)
	}
	participantID, ok := identifier.GetIdentifier().(*threadsv1.ParticipantIdentifier_ParticipantId)
	if !ok {
		t.Fatalf("expected participant id identifier, got %#v", identifier.GetIdentifier())
	}
	if participantID.ParticipantId != "agent-1" {
		t.Fatalf("unexpected participant id: %s", participantID.ParticipantId)
	}

	if _, err := participantIdentifier("@"); err == nil {
		t.Fatalf("expected error for empty nickname")
	}
	if _, err := participantIdentifier(" "); err == nil {
		t.Fatalf("expected error for empty participant")
	}
}

func TestToMessageView(t *testing.T) {
	createdAt := timestamppb.New(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC))
	msg := &threadsv1.Message{
		Id:        "msg-1",
		ThreadId:  "thread-1",
		SenderId:  "sender-1",
		Body:      "",
		FileIds:   nil,
		CreatedAt: createdAt,
	}
	view, err := toMessageView(msg, map[string]string{"thread-1": "ref"})
	if err != nil {
		t.Fatalf("toMessageView: %v", err)
	}
	if view.ThreadRef != "ref" || view.ID != "msg-1" {
		t.Fatalf("unexpected view: %#v", view)
	}

	msg.Id = ""
	if _, err := toMessageView(msg, nil); err == nil {
		t.Fatalf("expected error for missing message id")
	}
}

func TestParseMessageCreated(t *testing.T) {
	payload, err := structpb.NewStruct(map[string]any{
		"thread_id":  "thread-1",
		"message_id": "msg-1",
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	envelope := &notificationsv1.NotificationEnvelope{
		Event:   messageCreatedEvent,
		Payload: payload,
	}
	notification, ok, err := parseMessageCreated(envelope)
	if err != nil {
		t.Fatalf("parse notification: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok notification")
	}
	if notification.ThreadID != "thread-1" || notification.MessageID != "msg-1" {
		t.Fatalf("unexpected notification: %#v", notification)
	}

	envelope.Event = "other"
	if _, ok, err := parseMessageCreated(envelope); err != nil || ok {
		t.Fatalf("expected non-matching event")
	}

	if _, ok, err := parseMessageCreated(nil); err != nil || ok {
		t.Fatalf("expected nil envelope to be ignored")
	}
}

func TestPayloadString(t *testing.T) {
	payload, err := structpb.NewStruct(map[string]any{"thread_id": "thread-1"})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	value, err := payloadString(payload, "thread_id")
	if err != nil {
		t.Fatalf("payloadString: %v", err)
	}
	if value != "thread-1" {
		t.Fatalf("unexpected payload value: %s", value)
	}
	if _, err := payloadString(payload, "missing"); err == nil {
		t.Fatalf("expected error for missing key")
	}

	wrongType, err := structpb.NewStruct(map[string]any{"thread_id": 12})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if _, err := payloadString(wrongType, "thread_id"); err == nil {
		t.Fatalf("expected error for non-string payload")
	}

	emptyString, err := structpb.NewStruct(map[string]any{"thread_id": ""})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if _, err := payloadString(emptyString, "thread_id"); err == nil {
		t.Fatalf("expected error for empty payload string")
	}
}

type threadsGatewayRecorder struct {
	gatewayv1connect.UnimplementedThreadsGatewayHandler
	createRequest *threadsv1.CreateThreadRequest
	addRequests   []*threadsv1.AddParticipantRequest
}

func (r *threadsGatewayRecorder) CreateThread(_ context.Context, req *connect.Request[threadsv1.CreateThreadRequest]) (*connect.Response[threadsv1.CreateThreadResponse], error) {
	r.createRequest = req.Msg
	return connect.NewResponse(&threadsv1.CreateThreadResponse{
		Thread: &threadsv1.Thread{Id: "thread-1"},
	}), nil
}

func (r *threadsGatewayRecorder) AddParticipant(_ context.Context, req *connect.Request[threadsv1.AddParticipantRequest]) (*connect.Response[threadsv1.AddParticipantResponse], error) {
	r.addRequests = append(r.addRequests, req.Msg)
	return connect.NewResponse(&threadsv1.AddParticipantResponse{}), nil
}

func newThreadsGatewayRunContext(t *testing.T, handler gatewayv1connect.ThreadsGatewayHandler) *RunContext {
	t.Helper()
	path, apiHandler := gatewayv1connect.NewThreadsGatewayHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, apiHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &RunContext{
		Clients: &gateway.Clients{
			HTTPClient: server.Client(),
			BaseURL:    server.URL,
		},
		OutputFormat: output.FormatTable,
	}
}

func findAddParticipantByID(requests []*threadsv1.AddParticipantRequest, id string) *threadsv1.AddParticipantRequest {
	for _, req := range requests {
		if req == nil || req.Participant == nil {
			continue
		}
		if participantID, ok := req.Participant.GetIdentifier().(*threadsv1.ParticipantIdentifier_ParticipantId); ok {
			if participantID.ParticipantId == id {
				return req
			}
		}
	}
	return nil
}

func findAddParticipantByNickname(requests []*threadsv1.AddParticipantRequest, nickname string) *threadsv1.AddParticipantRequest {
	for _, req := range requests {
		if req == nil || req.Participant == nil {
			continue
		}
		if participantNickname, ok := req.Participant.GetIdentifier().(*threadsv1.ParticipantIdentifier_ParticipantNickname); ok {
			if participantNickname.ParticipantNickname == nickname {
				return req
			}
		}
	}
	return nil
}
