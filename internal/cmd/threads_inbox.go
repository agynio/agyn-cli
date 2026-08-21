package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"connectrpc.com/connect"

	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	threadsv1 "github.com/agynio/agyn-cli/gen/agynio/api/threads/v1"
)

// A thread delivers to one of two consumer surfaces, and which one is a
// property of the participant rather than a choice: users and apps get a
// message recipient and are notified on thread_participant:me, while an agent
// instance gets an inbox item and is notified on instance_inbox:me. The two are
// mutually exclusive -- what is delivered to one is absent from the other.
//
// This command spoke only the first, for every caller. Run inside an agent
// workload it subscribed to a room its events never reach and then read a table
// its messages are never written to, so waiting on a reply from another agent
// could only ever time out.
const instanceInboxSelfRoom = "instance_inbox:me"

// unreadSurface is one participant's side of that split: the room its arrivals
// are announced on, and the read and ack that go with it.
type unreadSurface interface {
	room() string
	fetch(ctx context.Context, targets []threadTarget) ([]*threadsv1.Message, error)
	ack(ctx context.Context, messages []messageView) error
}

// runningAsAgentInstance reports whether this process is an agent's own CLI.
// The workload sets AGYN_IDENTITY_ID to the instance it runs as, and an
// instance's identity id is that instance -- the same value the inbox
// authorizes against. Nothing but a workload sets it.
func runningAsAgentInstance() bool {
	return strings.TrimSpace(os.Getenv(agynIdentityIDEnv)) != ""
}

// callerRoom is the room this caller's own message.created events arrive on,
// for waits that read the thread itself rather than a delivery to them.
func callerRoom() string {
	if runningAsAgentInstance() {
		return instanceInboxSelfRoom
	}
	return threadParticipantSelfRoom
}

// unreadSurfaceFor picks the surface this caller is actually delivered on.
func unreadSurfaceFor(runContext *RunContext, participantID string) unreadSurface {
	if !runningAsAgentInstance() {
		return &participantSurface{
			client: gatewayv1connect.NewThreadsGatewayClient(
				runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...),
			participantID: participantID,
		}
	}
	return &inboxSurface{
		client: gatewayv1connect.NewAgentsGatewayClient(
			runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...),
		instanceID: participantID,
		itemIDs:    map[string]string{},
	}
}

// participantSurface is the user and app side: message recipients.
type participantSurface struct {
	client        gatewayv1connect.ThreadsGatewayClient
	participantID string
}

func (s *participantSurface) room() string { return threadParticipantSelfRoom }

func (s *participantSurface) fetch(ctx context.Context, targets []threadTarget) ([]*threadsv1.Message, error) {
	return fetchUnreadMessages(ctx, s.client, targets, s.participantID)
}

func (s *participantSurface) ack(ctx context.Context, messages []messageView) error {
	return ackMessages(ctx, s.client, s.participantID, messages)
}

// inboxSurface is the agent-instance side: inbox items.
//
// An ack addresses the item, not the message inside it, so the surface
// remembers which item each message arrived in.
type inboxSurface struct {
	client     gatewayv1connect.AgentsGatewayClient
	instanceID string

	mu      sync.Mutex
	itemIDs map[string]string // message id -> inbox item id
}

func (s *inboxSurface) room() string { return instanceInboxSelfRoom }

// fetch reads this instance's unacked items and presents the thread-sourced
// ones as the messages they were made from, so the rest of the command sees
// what a user would. Items from anywhere but a thread belong to no thread and
// are left for the daemon: this command is about threads.
func (s *inboxSurface) fetch(ctx context.Context, targets []threadTarget) ([]*threadsv1.Message, error) {
	allowed := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		allowed[target.ID] = struct{}{}
	}

	pageToken := ""
	all := []*threadsv1.Message{}
	for {
		resp, err := s.client.GetUnackedInboxItems(ctx, connect.NewRequest(&agentsv1.GetUnackedInboxItemsRequest{
			AgentInstanceId: s.instanceID,
			PageSize:        defaultPageSize,
			PageToken:       pageToken,
		}))
		if err != nil {
			return nil, fmt.Errorf("get unread inbox items: %w", err)
		}
		for _, item := range resp.Msg.GetItems() {
			if item == nil {
				return nil, fmt.Errorf("unread inbox item is nil")
			}
			if item.GetSourceKind() != agentsv1.InboxItemSourceKind_INBOX_ITEM_SOURCE_KIND_THREAD {
				continue
			}
			if len(allowed) > 0 {
				if _, ok := allowed[item.GetThreadId()]; !ok {
					continue
				}
			}
			message, err := s.messageFromItem(item)
			if err != nil {
				return nil, err
			}
			all = append(all, message)
		}
		pageToken = resp.Msg.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}
	return all, nil
}

func (s *inboxSurface) messageFromItem(item *agentsv1.InboxItem) (*threadsv1.Message, error) {
	itemID := item.GetId()
	if itemID == "" {
		return nil, fmt.Errorf("inbox item.id is required")
	}
	messageID := item.GetMessageId()
	if messageID == "" {
		return nil, fmt.Errorf("inbox item %s is thread-sourced but names no message", itemID)
	}
	s.mu.Lock()
	s.itemIDs[messageID] = itemID
	s.mu.Unlock()

	return &threadsv1.Message{
		Id:        messageID,
		ThreadId:  item.GetThreadId(),
		SenderId:  item.GetSenderId(),
		Body:      item.GetBody(),
		FileIds:   item.GetFileIds(),
		CreatedAt: item.GetAcceptedAt(),
	}, nil
}

func (s *inboxSurface) ack(ctx context.Context, messages []messageView) error {
	if len(messages) == 0 {
		return nil
	}
	itemIDs := make([]string, 0, len(messages))
	seen := map[string]struct{}{}
	s.mu.Lock()
	for _, message := range messages {
		itemID, ok := s.itemIDs[message.ID]
		if !ok {
			continue
		}
		if _, done := seen[itemID]; done {
			continue
		}
		seen[itemID] = struct{}{}
		itemIDs = append(itemIDs, itemID)
	}
	s.mu.Unlock()
	if len(itemIDs) == 0 {
		return nil
	}
	_, err := s.client.AckInboxItems(ctx, connect.NewRequest(&agentsv1.AckInboxItemsRequest{
		AgentInstanceId: s.instanceID,
		ItemIds:         itemIDs,
	}))
	if err != nil {
		return fmt.Errorf("ack inbox items: %w", err)
	}
	return nil
}
