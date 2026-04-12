package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	notificationsv1 "github.com/agynio/agyn-cli/gen/agynio/api/notifications/v1"
	threadsv1 "github.com/agynio/agyn-cli/gen/agynio/api/threads/v1"
	"github.com/agynio/agyn-cli/internal/output"
	threadrefs "github.com/agynio/agyn-cli/internal/threads"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	defaultPageSize     int32 = 20
	messageCreatedEvent       = "message.created"
	threadIDEnv               = "THREAD_ID"
	agentIDEnv                = "AGENT_ID"
)

var (
	threadsCreateRef   string
	threadsCreateAdd   []string
	threadsCreateSend  string
	threadsCreateWait  int
	threadsSendThread  string
	threadsSendMessage string
	threadsSendFiles   []string
	threadsSendWait    int
	threadsReadThreads []string
	threadsReadUnread  bool
	threadsReadWait    int
	threadsAddThread   string
	threadsAddValues   []string
	threadsAddPassive  bool
)

type messageView struct {
	ID        string    `json:"id" yaml:"id"`
	ThreadID  string    `json:"thread_id" yaml:"thread_id"`
	ThreadRef string    `json:"thread_ref,omitempty" yaml:"thread_ref,omitempty"`
	Sender    string    `json:"sender" yaml:"sender"`
	Body      string    `json:"body" yaml:"body"`
	FileIDs   []string  `json:"file_ids,omitempty" yaml:"file_ids,omitempty"`
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
}

type createOutput struct {
	ThreadID  string `json:"thread_id" yaml:"thread_id"`
	ThreadRef string `json:"thread_ref,omitempty" yaml:"thread_ref,omitempty"`
	MessageID string `json:"message_id,omitempty" yaml:"message_id,omitempty"`
}

type sendOutput struct {
	MessageID string `json:"message_id" yaml:"message_id"`
	ThreadID  string `json:"thread_id" yaml:"thread_id"`
}

type refEntry struct {
	Ref      string `json:"ref" yaml:"ref"`
	ThreadID string `json:"thread_id" yaml:"thread_id"`
}

type threadTarget struct {
	ID  string
	Ref string
}

type messageNotification struct {
	ThreadID  string
	MessageID string
}

var threadsCmd = &cobra.Command{
	Use:   "threads",
	Short: "Manage message threads",
}

func init() {
	threadsCmd.AddCommand(newThreadsCreateCmd())
	threadsCmd.AddCommand(newThreadsSendCmd())
	threadsCmd.AddCommand(newThreadsReadCmd())
	threadsCmd.AddCommand(newThreadsAddCmd())
	threadsCmd.AddCommand(newThreadsListCmd())
	rootCmd.AddCommand(threadsCmd)
}

func newThreadsCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new thread",
		RunE:  runThreadsCreate,
	}
	cmd.Flags().StringVar(&threadsCreateRef, "ref", "", "Local ref alias to store")
	cmd.Flags().StringArrayVar(&threadsCreateAdd, "add", nil, "Participant identity (@nickname or ID)")
	cmd.Flags().StringVar(&threadsCreateSend, "send", "", "Message to send after creating the thread")
	cmd.Flags().IntVar(&threadsCreateWait, "wait", 0, "Seconds to wait for a response")
	return cmd
}

func newThreadsSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to a thread",
		RunE:  runThreadsSend,
	}
	cmd.Flags().StringVar(&threadsSendThread, "thread", "", "Thread ref or ID")
	cmd.Flags().StringVar(&threadsSendMessage, "message", "", "Message body")
	cmd.Flags().StringArrayVar(&threadsSendFiles, "file", nil, "File ID to include (repeatable)")
	cmd.Flags().IntVar(&threadsSendWait, "wait", 0, "Seconds to wait for a response")
	return cmd
}

func newThreadsReadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read messages from a thread",
		RunE:  runThreadsRead,
	}
	cmd.Flags().StringArrayVar(&threadsReadThreads, "thread", nil, "Thread ref or ID (repeatable)")
	cmd.Flags().BoolVar(&threadsReadUnread, "unread", false, "Only unread messages")
	cmd.Flags().IntVar(&threadsReadWait, "wait", 0, "Seconds to wait for new messages")
	return cmd
}

func newThreadsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add participants to a thread",
		RunE:  runThreadsAdd,
	}
	cmd.Flags().StringVar(&threadsAddThread, "thread", "", "Thread ref or ID")
	cmd.Flags().StringArrayVar(&threadsAddValues, "participant", nil, "Participant identity (@nickname or ID)")
	cmd.Flags().BoolVar(&threadsAddPassive, "passive", true, "Mark added participants as passive")
	return cmd
}

func newThreadsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List local thread refs",
		RunE:  runThreadsList,
	}
	return cmd
}

func runThreadsCreate(cmd *cobra.Command, _ []string) error {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	if threadsCreateWait < 0 {
		return fmt.Errorf("wait must be non-negative")
	}
	refsStore, err := threadrefs.DefaultRefStore()
	if err != nil {
		return err
	}
	refs, err := refsStore.Load()
	if err != nil {
		return err
	}

	participantIDs, participantNicknames, err := splitParticipants(threadsCreateAdd)
	if err != nil {
		return err
	}
	agentID := strings.TrimSpace(os.Getenv(agentIDEnv))
	if agentID != "" {
		participantIDs = appendUnique(participantIDs, agentID)
	}
	if len(participantIDs) == 0 {
		return fmt.Errorf("at least one participant is required")
	}
	sendMessage := strings.TrimSpace(threadsCreateSend)
	if (sendMessage != "" || threadsCreateWait > 0) && agentID == "" {
		return fmt.Errorf("%s is required for this command", agentIDEnv)
	}

	threadsClient := gatewayv1connect.NewThreadsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	createResp, err := threadsClient.CreateThread(cmd.Context(), connect.NewRequest(&threadsv1.CreateThreadRequest{ParticipantIds: participantIDs}))
	if err != nil {
		return fmt.Errorf("create thread: %w", err)
	}
	thread := createResp.Msg.GetThread()
	if thread == nil || thread.GetId() == "" {
		return fmt.Errorf("create thread: response missing thread id")
	}
	threadID := thread.GetId()

	if threadsCreateRef != "" {
		refs[threadsCreateRef] = threadID
		if err := refsStore.Save(refs); err != nil {
			return err
		}
	}

	for _, nickname := range participantNicknames {
		if err := addParticipant(cmd.Context(), threadsClient, threadID, nickname, false); err != nil {
			return err
		}
	}

	var messageID string
	if sendMessage != "" {
		sendResp, err := threadsClient.SendMessage(cmd.Context(), connect.NewRequest(&threadsv1.SendMessageRequest{
			ThreadId: threadID,
			SenderId: agentID,
			Body:     sendMessage,
		}))
		if err != nil {
			return fmt.Errorf("send message: %w", err)
		}
		if sendResp.Msg.GetMessage() == nil || sendResp.Msg.GetMessage().GetId() == "" {
			return fmt.Errorf("send message: response missing message id")
		}
		messageID = sendResp.Msg.GetMessage().GetId()
	}

	if threadsCreateWait > 0 {
		return waitOutputAndAck(cmd.Context(), cmd, runContext, threadsClient, []threadTarget{{ID: threadID, Ref: threadsCreateRef}}, agentID, refs, time.Duration(threadsCreateWait)*time.Second, false)
	}

	if runContext.OutputFormat == output.FormatTable {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), threadID)
		return err
	}
	return output.Print(runContext.OutputFormat, createOutput{
		ThreadID:  threadID,
		ThreadRef: threadsCreateRef,
		MessageID: messageID,
	})
}

func runThreadsSend(cmd *cobra.Command, _ []string) error {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	if threadsSendWait < 0 {
		return fmt.Errorf("wait must be non-negative")
	}
	refsStore, err := threadrefs.DefaultRefStore()
	if err != nil {
		return err
	}
	refs, err := refsStore.Load()
	if err != nil {
		return err
	}
	threadInputs := []string{}
	if strings.TrimSpace(threadsSendThread) != "" {
		threadInputs = []string{threadsSendThread}
	}
	threadTargets, err := resolveThreadTargets(threadInputs, refs)
	if err != nil {
		return err
	}
	threadID := threadTargets[0].ID

	message := strings.TrimSpace(threadsSendMessage)
	if message == "" && len(threadsSendFiles) == 0 {
		return fmt.Errorf("message or file ids are required")
	}
	senderID, err := requireAgentID()
	if err != nil {
		return err
	}
	threadsClient := gatewayv1connect.NewThreadsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	sendResp, err := threadsClient.SendMessage(cmd.Context(), connect.NewRequest(&threadsv1.SendMessageRequest{
		ThreadId: threadID,
		SenderId: senderID,
		Body:     message,
		FileIds:  append([]string{}, threadsSendFiles...),
	}))
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	if sendResp.Msg.GetMessage() == nil || sendResp.Msg.GetMessage().GetId() == "" {
		return fmt.Errorf("send message: response missing message id")
	}
	messageID := sendResp.Msg.GetMessage().GetId()

	if threadsSendWait > 0 {
		return waitOutputAndAck(cmd.Context(), cmd, runContext, threadsClient, threadTargets, senderID, refs, time.Duration(threadsSendWait)*time.Second, false)
	}

	if runContext.OutputFormat == output.FormatTable {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), messageID)
		return err
	}
	return output.Print(runContext.OutputFormat, sendOutput{MessageID: messageID, ThreadID: threadID})
}

func runThreadsRead(cmd *cobra.Command, _ []string) error {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	if threadsReadWait < 0 {
		return fmt.Errorf("wait must be non-negative")
	}
	refsStore, err := threadrefs.DefaultRefStore()
	if err != nil {
		return err
	}
	refs, err := refsStore.Load()
	if err != nil {
		return err
	}
	threadTargets, err := resolveThreadTargets(threadsReadThreads, refs)
	if err != nil {
		return err
	}
	includeThreadLine := len(threadTargets) > 1

	threadsClient := gatewayv1connect.NewThreadsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	if threadsReadUnread {
		participantID, err := requireAgentID()
		if err != nil {
			return err
		}
		protoMessages, err := fetchUnreadMessages(cmd.Context(), threadsClient, threadTargets, participantID)
		if err != nil {
			return err
		}
		if len(protoMessages) == 0 && threadsReadWait > 0 {
			return waitOutputAndAck(cmd.Context(), cmd, runContext, threadsClient, threadTargets, participantID, refs, time.Duration(threadsReadWait)*time.Second, includeThreadLine)
		}
		return outputAndAck(cmd.Context(), cmd, runContext.OutputFormat, threadsClient, participantID, protoMessages, refs, includeThreadLine)
	}

	messages, err := fetchMessages(cmd.Context(), threadsClient, threadTargets)
	if err != nil {
		return err
	}
	if len(messages) == 0 && threadsReadWait > 0 {
		messages, err = waitForMessages(cmd.Context(), runContext, threadTargets, time.Duration(threadsReadWait)*time.Second, func(ctx context.Context) ([]*threadsv1.Message, error) {
			return fetchMessages(ctx, threadsClient, threadTargets)
		})
		if err != nil {
			return err
		}
	}
	view, err := toMessageViews(messages, refs)
	if err != nil {
		return err
	}
	return outputMessages(cmd, runContext.OutputFormat, view, includeThreadLine)
}

func runThreadsAdd(cmd *cobra.Command, _ []string) error {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	if len(threadsAddValues) == 0 {
		return fmt.Errorf("participant is required")
	}
	refsStore, err := threadrefs.DefaultRefStore()
	if err != nil {
		return err
	}
	refs, err := refsStore.Load()
	if err != nil {
		return err
	}
	threadInputs := []string{}
	if strings.TrimSpace(threadsAddThread) != "" {
		threadInputs = []string{threadsAddThread}
	}
	threadTargets, err := resolveThreadTargets(threadInputs, refs)
	if err != nil {
		return err
	}
	threadID := threadTargets[0].ID

	threadsClient := gatewayv1connect.NewThreadsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	for _, participant := range threadsAddValues {
		if err := addParticipant(cmd.Context(), threadsClient, threadID, participant, threadsAddPassive); err != nil {
			return err
		}
	}
	return nil
}

func runThreadsList(cmd *cobra.Command, _ []string) error {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	refsStore, err := threadrefs.DefaultRefStore()
	if err != nil {
		return err
	}
	refs, err := refsStore.Load()
	if err != nil {
		return err
	}
	entries := make([]refEntry, 0, len(refs))
	for ref, id := range refs {
		entries = append(entries, refEntry{Ref: ref, ThreadID: id})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Ref < entries[j].Ref
	})

	if runContext.OutputFormat == output.FormatTable {
		rows := make([][]string, 0, len(entries))
		for _, entry := range entries {
			rows = append(rows, []string{entry.Ref, entry.ThreadID})
		}
		return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"REF", "THREAD_ID"}, Rows: rows})
	}
	return output.Print(runContext.OutputFormat, entries)
}

func resolveThreadTargets(inputs []string, refs map[string]string) ([]threadTarget, error) {
	if len(inputs) == 0 {
		if envThread := strings.TrimSpace(os.Getenv(threadIDEnv)); envThread != "" {
			inputs = []string{envThread}
		} else {
			return nil, fmt.Errorf("thread is required")
		}
	}
	reverseRefs := map[string]string{}
	for ref, id := range refs {
		reverseRefs[id] = ref
	}
	seen := map[string]struct{}{}
	resolved := make([]threadTarget, 0, len(inputs))
	for _, input := range inputs {
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			return nil, fmt.Errorf("thread reference cannot be empty")
		}
		threadID := trimmed
		ref := ""
		if resolvedID, ok := threadrefs.ResolveRef(refs, trimmed); ok {
			threadID = resolvedID
			ref = trimmed
		} else if knownRef, ok := reverseRefs[threadID]; ok {
			ref = knownRef
		}
		if _, ok := seen[threadID]; ok {
			continue
		}
		seen[threadID] = struct{}{}
		resolved = append(resolved, threadTarget{ID: threadID, Ref: ref})
	}
	return resolved, nil
}

func splitParticipants(values []string) ([]string, []string, error) {
	ids := []string{}
	nicknames := []string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, nil, fmt.Errorf("participant cannot be empty")
		}
		if strings.HasPrefix(trimmed, "@") {
			nicknames = appendUnique(nicknames, trimmed)
			continue
		}
		ids = appendUnique(ids, trimmed)
	}
	return ids, nicknames, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func requireAgentID() (string, error) {
	agentID := strings.TrimSpace(os.Getenv(agentIDEnv))
	if agentID == "" {
		return "", fmt.Errorf("%s is required for this command", agentIDEnv)
	}
	return agentID, nil
}

func addParticipant(ctx context.Context, client gatewayv1connect.ThreadsGatewayClient, threadID, participant string, passive bool) error {
	identifier, err := participantIdentifier(participant)
	if err != nil {
		return err
	}
	_, err = client.AddParticipant(ctx, connect.NewRequest(&threadsv1.AddParticipantRequest{
		ThreadId:    threadID,
		Passive:     passive,
		Participant: identifier,
	}))
	if err != nil {
		return fmt.Errorf("add participant: %w", err)
	}
	return nil
}

func participantIdentifier(value string) (*threadsv1.ParticipantIdentifier, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("participant is required")
	}
	if strings.HasPrefix(trimmed, "@") {
		return &threadsv1.ParticipantIdentifier{
			Identifier: &threadsv1.ParticipantIdentifier_ParticipantNickname{ParticipantNickname: trimmed},
		}, nil
	}
	return &threadsv1.ParticipantIdentifier{
		Identifier: &threadsv1.ParticipantIdentifier_ParticipantId{ParticipantId: trimmed},
	}, nil
}

func fetchUnreadMessages(ctx context.Context, client gatewayv1connect.ThreadsGatewayClient, targets []threadTarget, participantID string) ([]*threadsv1.Message, error) {
	threadIDs := extractThreadIDs(targets)
	allowed := map[string]struct{}{}
	if len(threadIDs) > 1 {
		allowed = make(map[string]struct{}, len(threadIDs))
		for _, id := range threadIDs {
			allowed[id] = struct{}{}
		}
	}

	pageToken := ""
	all := []*threadsv1.Message{}
	for {
		request := &threadsv1.GetUnackedMessagesRequest{
			ParticipantId: participantID,
			PageSize:      defaultPageSize,
			PageToken:     pageToken,
		}
		if len(threadIDs) == 1 {
			request.ThreadId = proto.String(threadIDs[0])
		}
		resp, err := client.GetUnackedMessages(ctx, connect.NewRequest(request))
		if err != nil {
			return nil, fmt.Errorf("get unread messages: %w", err)
		}
		messages := resp.Msg.GetMessages()
		for _, msg := range messages {
			if msg == nil {
				return nil, fmt.Errorf("unread message is nil")
			}
			if len(allowed) > 0 {
				if _, ok := allowed[msg.GetThreadId()]; !ok {
					continue
				}
			}
			all = append(all, msg)
		}
		pageToken = resp.Msg.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}
	return all, nil
}

func fetchMessages(ctx context.Context, client gatewayv1connect.ThreadsGatewayClient, targets []threadTarget) ([]*threadsv1.Message, error) {
	all := make([]*threadsv1.Message, 0, len(targets)*int(defaultPageSize))
	for _, target := range targets {
		resp, err := client.GetMessages(ctx, connect.NewRequest(&threadsv1.GetMessagesRequest{
			ThreadId: target.ID,
			PageSize: defaultPageSize,
		}))
		if err != nil {
			return nil, fmt.Errorf("get messages for %s: %w", target.ID, err)
		}
		all = append(all, resp.Msg.GetMessages()...)
	}
	return all, nil
}

func extractThreadIDs(targets []threadTarget) []string {
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	return ids
}

func toMessageViews(messages []*threadsv1.Message, refs map[string]string) ([]messageView, error) {
	refIndex := map[string]string{}
	for ref, id := range refs {
		refIndex[id] = ref
	}
	views := make([]messageView, 0, len(messages))
	for _, msg := range messages {
		view, err := toMessageView(msg, refIndex)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].CreatedAt.Before(views[j].CreatedAt)
	})
	return views, nil
}

func toMessageView(msg *threadsv1.Message, refIndex map[string]string) (messageView, error) {
	if msg == nil {
		return messageView{}, fmt.Errorf("message is nil")
	}
	if msg.GetId() == "" {
		return messageView{}, fmt.Errorf("message.id is required")
	}
	if msg.GetThreadId() == "" {
		return messageView{}, fmt.Errorf("message.thread_id is required")
	}
	if msg.GetSenderId() == "" {
		return messageView{}, fmt.Errorf("message.sender_id is required")
	}
	createdAt := msg.GetCreatedAt()
	if createdAt == nil {
		return messageView{}, fmt.Errorf("message.created_at is required")
	}
	fileIDs := append([]string{}, msg.GetFileIds()...)
	return messageView{
		ID:        msg.GetId(),
		ThreadID:  msg.GetThreadId(),
		ThreadRef: refIndex[msg.GetThreadId()],
		Sender:    msg.GetSenderId(),
		Body:      msg.GetBody(),
		FileIDs:   fileIDs,
		CreatedAt: createdAt.AsTime(),
	}, nil
}

func outputMessages(cmd *cobra.Command, format output.Format, messages []messageView, includeThreadLine bool) error {
	if format == output.FormatTable {
		return renderMessages(cmd.OutOrStdout(), messages, includeThreadLine)
	}
	return output.Print(format, messages)
}

func renderMessages(w io.Writer, messages []messageView, includeThreadLine bool) error {
	for i, msg := range messages {
		if includeThreadLine {
			threadLabel := msg.ThreadID
			if msg.ThreadRef != "" {
				threadLabel = msg.ThreadRef
			}
			if _, err := fmt.Fprintf(w, "thread: %s\n", threadLabel); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "from: %s\n", msg.Sender); err != nil {
			return err
		}
		if msg.Body != "" {
			if _, err := fmt.Fprintln(w, msg.Body); err != nil {
				return err
			}
		}
		if len(msg.FileIDs) > 0 {
			if _, err := fmt.Fprintf(w, "files: %s\n", strings.Join(msg.FileIDs, ", ")); err != nil {
				return err
			}
		}
		if i < len(messages)-1 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}
	return nil
}

func outputAndAck(ctx context.Context, cmd *cobra.Command, format output.Format, client gatewayv1connect.ThreadsGatewayClient, participantID string, messages []*threadsv1.Message, refs map[string]string, includeThreadLine bool) error {
	view, err := toMessageViews(messages, refs)
	if err != nil {
		return err
	}
	if err := outputMessages(cmd, format, view, includeThreadLine); err != nil {
		return err
	}
	if len(view) == 0 {
		return nil
	}
	return ackMessages(ctx, client, participantID, view)
}

func waitOutputAndAck(ctx context.Context, cmd *cobra.Command, runContext *RunContext, client gatewayv1connect.ThreadsGatewayClient, targets []threadTarget, participantID string, refs map[string]string, timeout time.Duration, includeThreadLine bool) error {
	protoMessages, err := waitForUnreadMessages(ctx, runContext, targets, participantID, timeout)
	if err != nil {
		return err
	}
	return outputAndAck(ctx, cmd, runContext.OutputFormat, client, participantID, protoMessages, refs, includeThreadLine)
}

func ackMessages(ctx context.Context, client gatewayv1connect.ThreadsGatewayClient, participantID string, messages []messageView) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	seen := map[string]struct{}{}
	for _, msg := range messages {
		if _, ok := seen[msg.ID]; ok {
			continue
		}
		seen[msg.ID] = struct{}{}
		ids = append(ids, msg.ID)
	}
	_, err := client.AckMessages(ctx, connect.NewRequest(&threadsv1.AckMessagesRequest{
		ParticipantId: participantID,
		MessageIds:    ids,
	}))
	if err != nil {
		return fmt.Errorf("ack messages: %w", err)
	}
	return nil
}

func waitForUnreadMessages(ctx context.Context, runContext *RunContext, targets []threadTarget, participantID string, timeout time.Duration) ([]*threadsv1.Message, error) {
	threadsClient := gatewayv1connect.NewThreadsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	return waitForMessages(ctx, runContext, targets, timeout, func(ctx context.Context) ([]*threadsv1.Message, error) {
		return fetchUnreadMessages(ctx, threadsClient, targets, participantID)
	})
}

func waitForMessages(ctx context.Context, runContext *RunContext, targets []threadTarget, timeout time.Duration, fetch func(context.Context) ([]*threadsv1.Message, error)) ([]*threadsv1.Message, error) {
	notificationsClient := gatewayv1connect.NewNotificationsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	threadSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		threadSet[target.ID] = struct{}{}
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	messages, err := waitForNotificationMessages(waitCtx, notificationsClient, threadSet, fetch)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("wait timed out")
		}
		return nil, err
	}
	return messages, nil
}

func waitForNotificationMessages(ctx context.Context, client gatewayv1connect.NotificationsGatewayClient, targetThreads map[string]struct{}, fetch func(context.Context) ([]*threadsv1.Message, error)) ([]*threadsv1.Message, error) {
	events, errs, err := subscribeMessageNotifications(ctx, client, targetThreads)
	if err != nil {
		return nil, err
	}
	messages, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	if len(messages) > 0 {
		return messages, nil
	}
	fetchedIDs, err := messageIDSet(messages)
	if err != nil {
		return nil, err
	}
	buffered := drainNotifications(events)
	if hasPendingNotifications(buffered, fetchedIDs) {
		messages, err = fetch(ctx)
		if err != nil {
			return nil, err
		}
		if len(messages) > 0 {
			return messages, nil
		}
		fetchedIDs, err = messageIDSet(messages)
		if err != nil {
			return nil, err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err, ok := <-errs:
			if !ok {
				return nil, fmt.Errorf("notification stream closed")
			}
			if err != nil {
				return nil, err
			}
		case event, ok := <-events:
			if !ok {
				return nil, fmt.Errorf("notification stream closed")
			}
			if _, ok := fetchedIDs[event.MessageID]; ok {
				continue
			}
			messages, err = fetch(ctx)
			if err != nil {
				return nil, err
			}
			if len(messages) > 0 {
				return messages, nil
			}
			fetchedIDs, err = messageIDSet(messages)
			if err != nil {
				return nil, err
			}
		}
	}
}

func subscribeMessageNotifications(ctx context.Context, client gatewayv1connect.NotificationsGatewayClient, targetThreads map[string]struct{}) (<-chan messageNotification, <-chan error, error) {
	stream, err := client.Subscribe(ctx, connect.NewRequest(&notificationsv1.SubscribeRequest{}))
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe notifications: %w", err)
	}
	events := make(chan messageNotification, 32)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		for stream.Receive() {
			resp := stream.Msg()
			notification, ok, err := parseMessageCreated(resp.GetEnvelope())
			if err != nil {
				errs <- err
				return
			}
			if !ok {
				continue
			}
			if len(targetThreads) > 0 {
				if _, match := targetThreads[notification.ThreadID]; !match {
					continue
				}
			}
			select {
			case events <- notification:
			case <-ctx.Done():
				return
			}
		}
		if err := stream.Err(); err != nil {
			errs <- err
		}
	}()
	return events, errs, nil
}

func parseMessageCreated(envelope *notificationsv1.NotificationEnvelope) (messageNotification, bool, error) {
	if envelope == nil {
		return messageNotification{}, false, nil
	}
	if envelope.GetEvent() != messageCreatedEvent {
		return messageNotification{}, false, nil
	}
	payload := envelope.GetPayload()
	if payload == nil {
		return messageNotification{}, false, fmt.Errorf("notification payload is required")
	}
	threadID, err := payloadString(payload, "thread_id")
	if err != nil {
		return messageNotification{}, false, err
	}
	messageID, err := payloadString(payload, "message_id")
	if err != nil {
		return messageNotification{}, false, err
	}
	return messageNotification{ThreadID: threadID, MessageID: messageID}, true, nil
}

func payloadString(payload *structpb.Struct, key string) (string, error) {
	value, ok := payload.Fields[key]
	if !ok {
		return "", fmt.Errorf("notification payload missing %s", key)
	}
	stringValue, ok := value.AsInterface().(string)
	if !ok || strings.TrimSpace(stringValue) == "" {
		return "", fmt.Errorf("notification payload %s must be a non-empty string", key)
	}
	return stringValue, nil
}

func drainNotifications(events <-chan messageNotification) []messageNotification {
	buffer := []messageNotification{}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return buffer
			}
			buffer = append(buffer, event)
		default:
			return buffer
		}
	}
}

func hasPendingNotifications(events []messageNotification, fetched map[string]struct{}) bool {
	for _, event := range events {
		if _, ok := fetched[event.MessageID]; !ok {
			return true
		}
	}
	return false
}

func messageIDSet(messages []*threadsv1.Message) (map[string]struct{}, error) {
	ids := map[string]struct{}{}
	for _, msg := range messages {
		if msg == nil {
			return nil, fmt.Errorf("message is nil")
		}
		id := msg.GetId()
		if id == "" {
			return nil, fmt.Errorf("message.id is required")
		}
		ids[id] = struct{}{}
	}
	return ids, nil
}
