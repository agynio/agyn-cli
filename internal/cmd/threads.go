package cmd

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	threadsv1 "github.com/agynio/agyn-cli/gen/agynio/api/threads/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type threadOutput struct {
	ID        string `json:"id" yaml:"id"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
}

func newThreadsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "threads",
		Short: "Manage threads",
	}

	cmd.AddCommand(newThreadsCreateCmd())
	cmd.AddCommand(newThreadsListCmd())

	return cmd
}

func newThreadsCreateCmd() *cobra.Command {
	var participantIDs []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a thread",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			normalized, err := normalizeIDs(participantIDs)
			if err != nil {
				return err
			}

			client := gatewayv1connect.NewThreadsGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.CreateThread(cmd.Context(), connect.NewRequest(&threadsv1.CreateThreadRequest{
				ParticipantIds: normalized,
			}))
			if err != nil {
				return err
			}

			thread, err := threadOutputFrom(response.Msg.GetThread())
			if err != nil {
				return err
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "CREATED_AT"},
					Rows: [][]string{{
						thread.ID,
						thread.CreatedAt,
					}},
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, thread)
		},
	}

	cmd.Flags().StringSliceVar(&participantIDs, "participant-ids", nil, "Participant IDs")
	_ = cmd.MarkFlagRequired("participant-ids")

	return cmd
}

func newThreadsListCmd() *cobra.Command {
	var participantID string
	var pageSize int32
	var pageToken string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List threads",
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
			response, err := client.GetThreads(cmd.Context(), connect.NewRequest(&threadsv1.GetThreadsRequest{
				ParticipantId: participantID,
				PageSize:      pageSize,
				PageToken:     pageToken,
			}))
			if err != nil {
				return err
			}

			threads := response.Msg.GetThreads()
			outputs := make([]threadOutput, 0, len(threads))
			rows := make([][]string, 0, len(threads))
			for _, thread := range threads {
				outputData, err := threadOutputFrom(thread)
				if err != nil {
					return err
				}
				outputs = append(outputs, outputData)
				rows = append(rows, []string{
					outputData.ID,
					outputData.CreatedAt,
				})
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "CREATED_AT"},
					Rows:    rows,
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, outputs)
		},
	}

	cmd.Flags().StringVar(&participantID, "participant", "", "Participant ID")
	cmd.Flags().Int32Var(&pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "Page token")
	_ = cmd.MarkFlagRequired("participant")

	return cmd
}

func normalizeIDs(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, fmt.Errorf("participant-ids cannot be empty")
		}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("participant-ids is required")
	}
	return normalized, nil
}

func threadOutputFrom(thread *threadsv1.Thread) (threadOutput, error) {
	if thread == nil {
		return threadOutput{}, fmt.Errorf("thread missing from response")
	}
	return threadOutput{
		ID:        thread.GetId(),
		CreatedAt: formatTimestamp(thread.GetCreatedAt()),
	}, nil
}
