package cmd

import (
	"fmt"

	"connectrpc.com/connect"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	threadsv1 "github.com/agynio/agyn-cli/gen/agynio/api/threads/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type messageOutput struct {
	ID        string `json:"id" yaml:"id"`
	ThreadID  string `json:"thread_id" yaml:"thread_id"`
	SenderID  string `json:"sender_id" yaml:"sender_id"`
	Body      string `json:"body" yaml:"body"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
}

func newMessagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "messages",
		Short: "Manage messages",
	}

	cmd.AddCommand(newMessagesSendCmd())
	cmd.AddCommand(newMessagesListCmd())

	return cmd
}

func newMessagesSendCmd() *cobra.Command {
	var threadID string
	var senderID string
	var body string

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			client := gatewayv1connect.NewThreadsGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.SendMessage(cmd.Context(), connect.NewRequest(&threadsv1.SendMessageRequest{
				ThreadId: threadID,
				SenderId: senderID,
				Body:     body,
			}))
			if err != nil {
				return err
			}
			message, err := messageOutputFrom(response.Msg.GetMessage())
			if err != nil {
				return err
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "SENDER_ID", "BODY", "CREATED_AT"},
					Rows: [][]string{{
						message.ID,
						message.SenderID,
						message.Body,
						message.CreatedAt,
					}},
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, message)
		},
	}

	cmd.Flags().StringVar(&threadID, "thread", "", "Thread ID")
	cmd.Flags().StringVar(&senderID, "sender", "", "Sender ID")
	cmd.Flags().StringVar(&body, "body", "", "Message body")
	_ = cmd.MarkFlagRequired("thread")
	_ = cmd.MarkFlagRequired("sender")
	_ = cmd.MarkFlagRequired("body")

	return cmd
}

func newMessagesListCmd() *cobra.Command {
	var threadID string
	var pageSize int32
	var pageToken string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}
			if pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}

			client := gatewayv1connect.NewThreadsGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.GetMessages(cmd.Context(), connect.NewRequest(&threadsv1.GetMessagesRequest{
				ThreadId:  threadID,
				PageSize:  pageSize,
				PageToken: pageToken,
			}))
			if err != nil {
				return err
			}

			messages := response.Msg.GetMessages()
			outputs := make([]messageOutput, 0, len(messages))
			rows := make([][]string, 0, len(messages))
			for _, message := range messages {
				outputData, err := messageOutputFrom(message)
				if err != nil {
					return err
				}
				outputs = append(outputs, outputData)
				rows = append(rows, []string{
					outputData.ID,
					outputData.SenderID,
					outputData.Body,
					outputData.CreatedAt,
				})
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "SENDER_ID", "BODY", "CREATED_AT"},
					Rows:    rows,
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, outputs)
		},
	}

	cmd.Flags().StringVar(&threadID, "thread", "", "Thread ID")
	cmd.Flags().Int32Var(&pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "Page token")
	_ = cmd.MarkFlagRequired("thread")

	return cmd
}

func messageOutputFrom(message *threadsv1.Message) (messageOutput, error) {
	if message == nil {
		return messageOutput{}, fmt.Errorf("message missing from response")
	}
	return messageOutput{
		ID:        message.GetId(),
		ThreadID:  message.GetThreadId(),
		SenderID:  message.GetSenderId(),
		Body:      message.GetBody(),
		CreatedAt: formatTimestamp(message.GetCreatedAt()),
	}, nil
}
