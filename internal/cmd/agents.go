package cmd

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	"github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	threadrefs "github.com/agynio/agyn-cli/internal/threads"
	"github.com/spf13/cobra"
)

func newAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage agents",
	}
	cmd.AddCommand(newAgentsListCmd())
	cmd.AddCommand(newAgentsInstantiateCmd())
	return cmd
}

func newAgentsListCmd() *cobra.Command {
	var organizationID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			client := gatewayv1connect.NewAgentsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
			request := &agentsv1.ListAgentsRequest{PageSize: int32(maxAgentsPageSize)}
			if org := runContext.OrganizationID(organizationID); org != "" {
				request.OrganizationId = org
			}
			resp, err := client.ListAgents(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return fmt.Errorf("list agents: %w", err)
			}
			agents := resp.Msg.GetAgents()
			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, agents)
			}
			rows := make([][]string, 0, len(agents))
			for _, agent := range agents {
				handle := agent.GetNickname()
				if handle != "" {
					handle = "@" + handle
				}
				rows = append(rows, []string{agent.GetMeta().GetId(), agent.GetName(), handle})
			}
			return output.Table{Headers: []string{"ID", "NAME", "HANDLE"}, Rows: rows}.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&organizationID, "organization", "", "Organization ID")
	return cmd
}

const maxAgentsPageSize = 200

func newAgentsInstantiateCmd() *cobra.Command {
	var (
		label          string
		defaultThread  string
		organizationID string
	)
	cmd := &cobra.Command{
		Use:   "instantiate @HANDLE",
		Short: "Create an agent instance explicitly",
		Long: "Creates an instance of an agent class. Usually unnecessary -- adding an\n" +
			"agent to a thread creates one lazily -- but useful when an instance needs\n" +
			"a chosen label or a default thread up front.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return instantiateAgent(cmd, args[0], label, defaultThread, organizationID)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "Handle suffix for the instance, without the leading #")
	cmd.Flags().StringVar(&defaultThread, "default-thread", "", "Thread ref or ID the instance sends to when none is named")
	cmd.Flags().StringVar(&organizationID, "organization", "", "Organization ID")
	return cmd
}

func instantiateAgent(cmd *cobra.Command, handle, label, defaultThread, organizationID string) error {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return err
	}
	client := gatewayv1connect.NewAgentsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)

	agentID, err := resolveAgentID(cmd.Context(), client, handle, runContext.OrganizationID(organizationID))
	if err != nil {
		return err
	}

	request := &agentsv1.CreateInstanceRequest{AgentId: agentID}
	if trimmed := strings.TrimSpace(label); trimmed != "" {
		request.Label = &trimmed
	}
	// default_thread_id, not context.thread_id. The context field reports the
	// thread that happened to create the instance and is only consulted through
	// the class policy -- an agent whose policy is NONE would ignore it. Naming
	// the thread on the command line is a deliberate choice, which is what
	// default_thread_id means and why it wins over the policy.
	if trimmed := strings.TrimSpace(defaultThread); trimmed != "" {
		threadID, err := resolveThreadReference(trimmed)
		if err != nil {
			return err
		}
		request.DefaultThreadId = &threadID
	}

	resp, err := client.CreateInstance(cmd.Context(), connect.NewRequest(request))
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}
	instance := resp.Msg.GetInstance()
	if runContext.OutputFormat != output.FormatTable {
		return output.Print(runContext.OutputFormat, instance)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", instance.GetMeta().GetId(), instance.GetHandle())
	return nil
}

// resolveAgentID turns "@nickname" into an agent id. A bare id is passed
// through: the argument is documented as a handle, but refusing an id the user
// already has would be gratuitous.
func resolveAgentID(ctx context.Context, client gatewayv1connect.AgentsGatewayClient, handle, organizationID string) (string, error) {
	trimmed := strings.TrimSpace(handle)
	if trimmed == "" || trimmed == "@" {
		return "", fmt.Errorf("agent handle is required")
	}
	if !strings.HasPrefix(trimmed, "@") {
		return trimmed, nil
	}
	nickname := strings.TrimPrefix(trimmed, "@")

	request := &agentsv1.ListAgentsRequest{PageSize: int32(maxAgentsPageSize)}
	if organizationID != "" {
		request.OrganizationId = organizationID
	}
	resp, err := client.ListAgents(ctx, connect.NewRequest(request))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", trimmed, err)
	}
	for _, agent := range resp.Msg.GetAgents() {
		if agent.GetNickname() == nickname {
			return agent.GetMeta().GetId(), nil
		}
	}
	return "", fmt.Errorf("no agent with handle %s", trimmed)
}

// resolveThreadReference accepts either a local ref stored by `threads create`
// or a thread id, matching what every --thread flag takes.
func resolveThreadReference(value string) (string, error) {
	store, err := threadrefs.DefaultRefStore()
	if err != nil {
		return "", err
	}
	refs, err := store.Load()
	if err != nil {
		return "", err
	}
	if id, ok := refs[value]; ok {
		return id, nil
	}
	return value, nil
}

func init() {
	rootCmd.AddCommand(newAgentsCmd())
}
