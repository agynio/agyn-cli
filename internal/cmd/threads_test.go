package cmd

import (
	"testing"
	"time"

	notificationsv1 "github.com/agynio/agyn-cli/gen/agynio/api/notifications/v1"
	threadsv1 "github.com/agynio/agyn-cli/gen/agynio/api/threads/v1"
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

func TestParticipantIdentifiersFromValues(t *testing.T) {
	participants, err := participantIdentifiersFromValues([]string{"@alice", "user-1", "@bob", "user-1"})
	if err != nil {
		t.Fatalf("participants from values: %v", err)
	}
	if len(participants) != 3 {
		t.Fatalf("expected 3 participants, got %d", len(participants))
	}
	nickname, ok := participants[0].GetIdentifier().(*threadsv1.ParticipantIdentifier_ParticipantNickname)
	if !ok {
		t.Fatalf("expected nickname identifier, got %#v", participants[0].GetIdentifier())
	}
	if nickname.ParticipantNickname != "@alice" {
		t.Fatalf("unexpected nickname: %s", nickname.ParticipantNickname)
	}
	participantID, ok := participants[1].GetIdentifier().(*threadsv1.ParticipantIdentifier_ParticipantId)
	if !ok {
		t.Fatalf("expected participant id identifier, got %#v", participants[1].GetIdentifier())
	}
	if participantID.ParticipantId != "user-1" {
		t.Fatalf("unexpected participant id: %s", participantID.ParticipantId)
	}
	nickname, ok = participants[2].GetIdentifier().(*threadsv1.ParticipantIdentifier_ParticipantNickname)
	if !ok {
		t.Fatalf("expected nickname identifier, got %#v", participants[2].GetIdentifier())
	}
	if nickname.ParticipantNickname != "@bob" {
		t.Fatalf("unexpected nickname: %s", nickname.ParticipantNickname)
	}
	if _, err := participantIdentifiersFromValues([]string{""}); err == nil {
		t.Fatalf("expected error for empty participant")
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
