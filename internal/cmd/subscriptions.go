package cmd

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	llmv1 "github.com/agynio/agyn-cli/gen/agynio/api/llm/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

const subscriptionPageSize = 200

type subscriptionOutput struct {
	ID             string `json:"id" yaml:"id"`
	Name           string `json:"name" yaml:"name"`
	Vendor         string `json:"vendor" yaml:"vendor"`
	SecretID       string `json:"secret_id" yaml:"secret_id"`
	AccountID      string `json:"account_id,omitempty" yaml:"account_id,omitempty"`
	OrganizationID string `json:"organization_id" yaml:"organization_id"`
	CreatedAt      string `json:"created_at" yaml:"created_at"`
	UpdatedAt      string `json:"updated_at" yaml:"updated_at"`
}

type subscriptionAttachmentOutput struct {
	ID             string `json:"id" yaml:"id"`
	SubscriptionID string `json:"subscription_id" yaml:"subscription_id"`
	Vendor         string `json:"vendor" yaml:"vendor"`
	AgentID        string `json:"agent_id,omitempty" yaml:"agent_id,omitempty"`
	EnvironmentID  string `json:"environment_id,omitempty" yaml:"environment_id,omitempty"`
	CreatedAt      string `json:"created_at" yaml:"created_at"`
	UpdatedAt      string `json:"updated_at" yaml:"updated_at"`
}

type subscriptionArgs struct {
	organizationID string
	name           string
	vendor         string
	secret         string
	accountID      string
	agentID        string
	environmentID  string
	subscriptionID string
	pageSize       int32
	pageToken      string
}

func newSubscriptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subscriptions",
		Aliases: []string{"subscription"},
		Short:   "Manage vendor subscriptions and what they are attached to",
		Long: "A subscription is an organization's own plan with an agent CLI vendor,\n" +
			"held as a reference to a secret. Attach one to an environment or an agent\n" +
			"and workloads there reach the vendor on it; the token is injected in\n" +
			"flight and never reaches the container.",
	}
	cmd.AddCommand(newSubscriptionsCreateCmd())
	cmd.AddCommand(newSubscriptionsListCmd())
	cmd.AddCommand(newSubscriptionsShowCmd())
	cmd.AddCommand(newSubscriptionsUpdateCmd())
	cmd.AddCommand(newSubscriptionsDeleteCmd())
	cmd.AddCommand(newSubscriptionsAttachCmd())
	cmd.AddCommand(newSubscriptionsDetachCmd())
	cmd.AddCommand(newSubscriptionsAttachmentsCmd())

	return cmd
}

func newSubscriptionsCreateCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			client, runContext, err := llmGatewayClient(cmd)
			if err != nil {
				return err
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			vendor, err := parseVendor(args.vendor)
			if err != nil {
				return err
			}
			response, err := client.CreateSubscription(cmd.Context(), connect.NewRequest(&llmv1.CreateSubscriptionRequest{
				OrganizationId: organizationID,
				Name:           positional[0],
				Vendor:         vendor,
				SecretId:       strings.TrimSpace(args.secret),
				AccountId:      strings.TrimSpace(args.accountID),
			}))
			if err != nil {
				return err
			}
			return output.Print(runContext.OutputFormat, subscriptionOutputFrom(response.Msg.GetSubscription()))
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.vendor, "vendor", "", "anthropic or openai")
	cmd.Flags().StringVar(&args.secret, "secret", "", "Secret ID holding the subscription token")
	cmd.Flags().StringVar(&args.accountID, "account-id", "", "Vendor account identifier, when the vendor's API requires one")
	_ = cmd.MarkFlagRequired("vendor")
	_ = cmd.MarkFlagRequired("secret")
	return cmd
}

func newSubscriptionsListCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List subscriptions in the organization",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := llmGatewayClient(cmd)
			if err != nil {
				return err
			}
			if args.pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			response, err := client.ListSubscriptions(cmd.Context(), connect.NewRequest(&llmv1.ListSubscriptionsRequest{
				OrganizationId: organizationID,
				PageSize:       args.pageSize,
				PageToken:      args.pageToken,
			}))
			if err != nil {
				return err
			}
			subscriptions := response.Msg.GetSubscriptions()
			outputs := make([]subscriptionOutput, 0, len(subscriptions))
			rows := make([][]string, 0, len(subscriptions))
			for _, subscription := range subscriptions {
				out := subscriptionOutputFrom(subscription)
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.Name, out.Vendor, out.SecretID, out.UpdatedAt})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{
					Headers: []string{"ID", "NAME", "VENDOR", "SECRET", "UPDATED_AT"},
					Rows:    rows,
				})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

func newSubscriptionsShowCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			client, runContext, subscription, err := resolveSubscriptionArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			_ = client
			return output.Print(runContext.OutputFormat, subscriptionOutputFrom(subscription))
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

func newSubscriptionsUpdateCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "update NAME",
		Short: "Change a subscription; flags not given are left as they are",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			client, runContext, subscription, err := resolveSubscriptionArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			request := &llmv1.UpdateSubscriptionRequest{Id: subscription.GetMeta().GetId()}
			if name := strings.TrimSpace(args.name); cmd.Flags().Changed("name") {
				request.Name = &name
			}
			if cmd.Flags().Changed("secret") {
				secret := strings.TrimSpace(args.secret)
				request.SecretId = &secret
			}
			// Cleared by passing it empty, which is why this checks the flag
			// rather than the value: a vendor account can go away.
			if cmd.Flags().Changed("account-id") {
				accountID := strings.TrimSpace(args.accountID)
				request.AccountId = &accountID
			}
			response, err := client.UpdateSubscription(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			return output.Print(runContext.OutputFormat, subscriptionOutputFrom(response.Msg.GetSubscription()))
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.name, "name", "", "New name")
	cmd.Flags().StringVar(&args.secret, "secret", "", "Secret ID holding the subscription token")
	cmd.Flags().StringVar(&args.accountID, "account-id", "", "Vendor account identifier; pass empty to clear")
	return cmd
}

func newSubscriptionsDeleteCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			client, _, subscription, err := resolveSubscriptionArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			if _, err := client.DeleteSubscription(cmd.Context(), connect.NewRequest(&llmv1.DeleteSubscriptionRequest{
				Id: subscription.GetMeta().GetId(),
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted subscription %s\n", positional[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

// Attaching to an environment is `agyn environments subscriptions attach`,
// where the environment is named rather than typed as a UUID. These two cover
// agent scope, which has no environment to hang off.
func newSubscriptionsAttachCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "attach NAME --agent AGENT_ID",
		Short: "Attach a subscription to an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			client, runContext, subscription, err := resolveSubscriptionArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			agentID := strings.TrimSpace(args.agentID)
			if agentID == "" {
				return fmt.Errorf("--agent is required; attach to an environment with `agyn environments subscriptions attach`")
			}
			response, err := client.CreateSubscriptionAttachment(cmd.Context(), connect.NewRequest(&llmv1.CreateSubscriptionAttachmentRequest{
				SubscriptionId: subscription.GetMeta().GetId(),
				Target:         &llmv1.CreateSubscriptionAttachmentRequest_AgentId{AgentId: agentID},
			}))
			if err != nil {
				return err
			}
			return output.Print(runContext.OutputFormat, subscriptionAttachmentOutputFrom(response.Msg.GetSubscriptionAttachment()))
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.agentID, "agent", "", "Agent ID to attach to")
	_ = cmd.MarkFlagRequired("agent")
	return cmd
}

func newSubscriptionsDetachCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "detach NAME --agent AGENT_ID",
		Short: "Detach a subscription from an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			client, _, subscription, err := resolveSubscriptionArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			agentID := strings.TrimSpace(args.agentID)
			if agentID == "" {
				return fmt.Errorf("--agent is required; detach from an environment with `agyn environments subscriptions detach`")
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			attachment, err := findSubscriptionAttachment(cmd, client, organizationID, subscription.GetMeta().GetId(), &agentID, nil)
			if err != nil {
				return err
			}
			if _, err := client.DeleteSubscriptionAttachment(cmd.Context(), connect.NewRequest(&llmv1.DeleteSubscriptionAttachmentRequest{
				Id: attachment.GetMeta().GetId(),
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Detached %s from agent %s\n", positional[0], agentID)
			return nil
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.agentID, "agent", "", "Agent ID to detach from")
	_ = cmd.MarkFlagRequired("agent")
	return cmd
}

func newSubscriptionsAttachmentsCmd() *cobra.Command {
	args := &subscriptionArgs{}
	cmd := &cobra.Command{
		Use:   "attachments",
		Short: "List subscription attachments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := llmGatewayClient(cmd)
			if err != nil {
				return err
			}
			if args.pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			request := &llmv1.ListSubscriptionAttachmentsRequest{
				OrganizationId: organizationID,
				PageSize:       args.pageSize,
				PageToken:      args.pageToken,
			}
			// All three narrow independently, so unlike attach they are not
			// mutually exclusive.
			if subscriptionID := strings.TrimSpace(args.subscriptionID); subscriptionID != "" {
				request.SubscriptionId = &subscriptionID
			}
			if agentID := strings.TrimSpace(args.agentID); agentID != "" {
				request.AgentId = &agentID
			}
			if environmentID := strings.TrimSpace(args.environmentID); environmentID != "" {
				request.EnvironmentId = &environmentID
			}
			response, err := client.ListSubscriptionAttachments(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			attachments := response.Msg.GetSubscriptionAttachments()
			outputs := make([]subscriptionAttachmentOutput, 0, len(attachments))
			rows := make([][]string, 0, len(attachments))
			for _, attachment := range attachments {
				out := subscriptionAttachmentOutputFrom(attachment)
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.SubscriptionID, out.Vendor, attachmentTarget(out), out.UpdatedAt})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{
					Headers: []string{"ID", "SUBSCRIPTION", "VENDOR", "TARGET", "UPDATED_AT"},
					Rows:    rows,
				})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.subscriptionID, "subscription", "", "Only attachments of this subscription")
	cmd.Flags().StringVar(&args.agentID, "agent", "", "Only attachments on this agent")
	cmd.Flags().StringVar(&args.environmentID, "environment", "", "Only attachments on this environment")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

// resolveSubscriptionArg maps a name onto the subscription, the way every other
// resource group addresses its members. A UUID is accepted too, so an id copied
// out of `attachments` output works without a lookup.
func resolveSubscriptionArg(cmd *cobra.Command, organizationIDFlag, name string) (gatewayv1connect.LLMGatewayClient, *RunContext, *llmv1.Subscription, error) {
	client, runContext, err := llmGatewayClient(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	organizationID, err := organizationIDForCommand(cmd, organizationIDFlag)
	if err != nil {
		return nil, nil, nil, err
	}
	subscription, err := findSubscription(cmd, client, organizationID, name)
	if err != nil {
		return nil, nil, nil, err
	}
	return client, runContext, subscription, nil
}

func findSubscription(cmd *cobra.Command, client gatewayv1connect.LLMGatewayClient, organizationID, name string) (*llmv1.Subscription, error) {
	wanted := strings.TrimSpace(name)
	if wanted == "" {
		return nil, fmt.Errorf("subscription name is required")
	}
	request := &llmv1.ListSubscriptionsRequest{OrganizationId: organizationID, PageSize: subscriptionPageSize}
	for {
		response, err := client.ListSubscriptions(cmd.Context(), connect.NewRequest(request))
		if err != nil {
			return nil, err
		}
		for _, subscription := range response.Msg.GetSubscriptions() {
			if subscription.GetName() == wanted || subscription.GetMeta().GetId() == wanted {
				return subscription, nil
			}
		}
		request.PageToken = response.Msg.GetNextPageToken()
		if request.PageToken == "" {
			return nil, fmt.Errorf("no subscription named %q in this organization", wanted)
		}
	}
}

// findSubscriptionAttachment locates the one attachment of a subscription on a
// target. Attachments are unique on (vendor, target), so there is at most one.
func findSubscriptionAttachment(cmd *cobra.Command, client gatewayv1connect.LLMGatewayClient, organizationID, subscriptionID string, agentID, environmentID *string) (*llmv1.SubscriptionAttachment, error) {
	request := &llmv1.ListSubscriptionAttachmentsRequest{
		OrganizationId: organizationID,
		SubscriptionId: &subscriptionID,
		AgentId:        agentID,
		EnvironmentId:  environmentID,
		PageSize:       subscriptionPageSize,
	}
	for {
		response, err := client.ListSubscriptionAttachments(cmd.Context(), connect.NewRequest(request))
		if err != nil {
			return nil, err
		}
		if attachments := response.Msg.GetSubscriptionAttachments(); len(attachments) > 0 {
			return attachments[0], nil
		}
		request.PageToken = response.Msg.GetNextPageToken()
		if request.PageToken == "" {
			return nil, fmt.Errorf("the subscription is not attached to that target")
		}
	}
}

func llmGatewayClient(cmd *cobra.Command) (gatewayv1connect.LLMGatewayClient, *RunContext, error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return nil, nil, err
	}
	if runContext.Clients == nil {
		return nil, nil, fmt.Errorf("gateway client unavailable")
	}
	client := gatewayv1connect.NewLLMGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	return client, runContext, nil
}

func parseVendor(raw string) (llmv1.Vendor, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "anthropic":
		return llmv1.Vendor_VENDOR_ANTHROPIC, nil
	case "openai":
		return llmv1.Vendor_VENDOR_OPENAI, nil
	default:
		return llmv1.Vendor_VENDOR_UNSPECIFIED, fmt.Errorf("unknown vendor %q: expected anthropic or openai", raw)
	}
}

func vendorName(vendor llmv1.Vendor) string {
	switch vendor {
	case llmv1.Vendor_VENDOR_ANTHROPIC:
		return "anthropic"
	case llmv1.Vendor_VENDOR_OPENAI:
		return "openai"
	default:
		return "unspecified"
	}
}

func subscriptionOutputFrom(subscription *llmv1.Subscription) subscriptionOutput {
	meta := subscription.GetMeta()
	return subscriptionOutput{
		ID:             meta.GetId(),
		Name:           subscription.GetName(),
		Vendor:         vendorName(subscription.GetVendor()),
		SecretID:       subscription.GetSecretId(),
		AccountID:      subscription.GetAccountId(),
		OrganizationID: subscription.GetOrganizationId(),
		CreatedAt:      formatTimestamp(meta.GetCreatedAt()),
		UpdatedAt:      formatTimestamp(meta.GetUpdatedAt()),
	}
}

func subscriptionAttachmentOutputFrom(attachment *llmv1.SubscriptionAttachment) subscriptionAttachmentOutput {
	meta := attachment.GetMeta()
	return subscriptionAttachmentOutput{
		ID:             meta.GetId(),
		SubscriptionID: attachment.GetSubscriptionId(),
		Vendor:         vendorName(attachment.GetVendor()),
		AgentID:        attachment.GetAgentId(),
		EnvironmentID:  attachment.GetEnvironmentId(),
		CreatedAt:      formatTimestamp(meta.GetCreatedAt()),
		UpdatedAt:      formatTimestamp(meta.GetUpdatedAt()),
	}
}

func attachmentTarget(out subscriptionAttachmentOutput) string {
	if out.AgentID != "" {
		return "agent " + out.AgentID
	}
	return "environment " + out.EnvironmentID
}

func init() {
	rootCmd.AddCommand(newSubscriptionsCmd())
}
