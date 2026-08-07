package cmd

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

const environmentPageSize = 200

type environmentOutput struct {
	ID                string   `json:"id" yaml:"id"`
	Name              string   `json:"name" yaml:"name"`
	RunnerID          string   `json:"runner_id,omitempty" yaml:"runner_id,omitempty"`
	Flavor            string   `json:"flavor,omitempty" yaml:"flavor,omitempty"`
	WorkspaceImage    string   `json:"workspace_image,omitempty" yaml:"workspace_image,omitempty"`
	AgentRuntimeImage string   `json:"agent_runtime_image,omitempty" yaml:"agent_runtime_image,omitempty"`
	Availability      string   `json:"availability" yaml:"availability"`
	LLMMode           string   `json:"llm_mode" yaml:"llm_mode"`
	LLMAllowedModels  []string `json:"llm_allowed_models,omitempty" yaml:"llm_allowed_models,omitempty"`
}

type environmentArgs struct {
	organizationID    string
	runner            string
	flavor            string
	workspaceImage    string
	agentRuntimeImage string
	availability      string
	llmMode           string
	allowedModels     []string
}

func newEnvironmentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "environments",
		Aliases: []string{"environment", "env"},
		Short:   "Manage environments and everything they contain",
		Long: "An environment is a runner, a flavor, images, and the volumes, MCP servers,\n" +
			"init scripts and variables a workload in it carries. Any organization member\n" +
			"can create one; what you may read and change on someone else's follows the\n" +
			"role you hold on it.",
	}
	cmd.AddCommand(newEnvironmentsListCmd())
	cmd.AddCommand(newEnvironmentsShowCmd())
	cmd.AddCommand(newEnvironmentsCreateCmd())
	cmd.AddCommand(newEnvironmentsUpdateCmd())
	cmd.AddCommand(newEnvironmentsDeleteCmd())
	cmd.AddCommand(newEnvironmentVolumesCmd())
	cmd.AddCommand(newEnvironmentMcpsCmd())
	cmd.AddCommand(newEnvironmentInitScriptsCmd())
	cmd.AddCommand(newEnvironmentVarsCmd())
	cmd.AddCommand(newEnvironmentRolesCmd())
	cmd.AddCommand(newEnvironmentSubscriptionsCmd())
	return cmd
}

func newEnvironmentsListCmd() *cobra.Command {
	args := &environmentArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List environments in the organization",
		RunE: func(cmd *cobra.Command, _ []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			organizationID, err := clients.resolveOrganizationID(ctx, args.organizationID)
			if err != nil {
				return err
			}
			environments, err := clients.listEnvironments(ctx, organizationID)
			if err != nil {
				return err
			}
			outputs := make([]environmentOutput, 0, len(environments))
			rows := make([][]string, 0, len(environments))
			for _, environment := range environments {
				out := environmentOutputFrom(environment)
				outputs = append(outputs, out)
				rows = append(rows, []string{out.Name, out.RunnerID, out.Flavor, out.WorkspaceImage, out.Availability})
			}
			if clients.runContext.OutputFormat == output.FormatTable {
				return output.Print(clients.runContext.OutputFormat, output.Table{
					Headers: []string{"NAME", "RUNNER", "FLAVOR", "WORKSPACE_IMAGE", "AVAILABILITY"},
					Rows:    rows,
				})
			}
			return output.Print(clients.runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

func newEnvironmentsShowCmd() *cobra.Command {
	args := &environmentArgs{}
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show an environment and its contents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			organizationID, err := clients.resolveOrganizationID(ctx, args.organizationID)
			if err != nil {
				return err
			}
			environment, err := clients.findEnvironment(ctx, organizationID, positional[0])
			if err != nil {
				return err
			}
			out := environmentOutputFrom(environment)
			if clients.runContext.OutputFormat != output.FormatTable {
				return output.Print(clients.runContext.OutputFormat, out)
			}
			writer := cmd.OutOrStdout()
			fmt.Fprintf(writer, "Name:          %s\n", out.Name)
			fmt.Fprintf(writer, "Runner:        %s\n", out.RunnerID)
			fmt.Fprintf(writer, "Flavor:        %s\n", valueOrDash(out.Flavor))
			fmt.Fprintf(writer, "Workspace:     %s\n", valueOrDash(out.WorkspaceImage))
			fmt.Fprintf(writer, "Agent runtime: %s\n", valueOrDash(out.AgentRuntimeImage))
			fmt.Fprintf(writer, "Availability:  %s\n", out.Availability)
			fmt.Fprintf(writer, "LLM mode:      %s\n", out.LLMMode)
			if len(out.LLMAllowedModels) > 0 {
				fmt.Fprintf(writer, "Allowed models: %s\n", strings.Join(out.LLMAllowedModels, ", "))
			}

			// Printing nothing under "Volumes" reads as "declares no storage",
			// which is a different fact from "you cannot see the storage it
			// declares" -- and one that decides whether work survives a restart.
			contents, err := clients.environmentContents(ctx, environment.GetMeta().GetId())
			if err != nil {
				if isPermissionDenied(err) {
					fmt.Fprintf(writer, "\nConfiguration is not visible to you: you hold no role on this environment.\n")
					return nil
				}
				return err
			}
			return printEnvironmentContents(writer, contents)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

func newEnvironmentsCreateCmd() *cobra.Command {
	args := &environmentArgs{}
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an environment; you become its owner",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			organizationID, err := clients.resolveOrganizationID(ctx, args.organizationID)
			if err != nil {
				return err
			}
			availability, err := parseAvailability(args.availability)
			if err != nil {
				return err
			}
			request := &agentsv1.CreateEnvironmentRequest{
				OrganizationId:   organizationID,
				Name:             positional[0],
				RunnerId:         strings.TrimSpace(args.runner),
				Flavor:           strings.TrimSpace(args.flavor),
				Availability:     availability,
				LlmAllowedModels: args.allowedModels,
			}
			if raw := strings.TrimSpace(args.llmMode); raw != "" {
				mode, err := parseLLMMode(raw)
				if err != nil {
					return err
				}
				request.LlmMode = mode
			}
			imageID, tag, err := splitImageReference(args.workspaceImage)
			if err != nil {
				return fmt.Errorf("--workspace-image: %w", err)
			}
			request.WorkspaceImageId, request.WorkspaceImageTag = imageID, tag
			if strings.TrimSpace(args.agentRuntimeImage) != "" {
				runtimeID, runtimeTag, err := splitImageReference(args.agentRuntimeImage)
				if err != nil {
					return fmt.Errorf("--agent-runtime-image: %w", err)
				}
				request.AgentRuntimeImageId, request.AgentRuntimeImageTag = runtimeID, runtimeTag
			}
			response, err := clients.agents.CreateEnvironment(ctx, connect.NewRequest(request))
			if err != nil {
				return err
			}
			// A flavor is resolved at every workload start, so naming one the
			// runner has not reported yet is allowed: platform resources and
			// runner configuration may be applied in either order.
			if flavor := strings.TrimSpace(args.flavor); flavor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Flavor %q is resolved at each workload start; if runner %s does not report it, the environment is unschedulable until it does.\n",
					flavor, request.GetRunnerId())
			}
			return output.Print(clients.runContext.OutputFormat, environmentOutputFrom(response.Msg.GetEnvironment()))
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.runner, "runner", "", "Runner ID workloads are placed on")
	cmd.Flags().StringVar(&args.flavor, "flavor", "", "Flavor name in the runner's catalog")
	cmd.Flags().StringVar(&args.workspaceImage, "workspace-image", "", "Workspace image as IMAGE_ID:TAG")
	cmd.Flags().StringVar(&args.agentRuntimeImage, "agent-runtime-image", "", "Agent runtime image as IMAGE_ID:TAG")
	cmd.Flags().StringVar(&args.availability, "availability", "", "internal or private")
	cmd.Flags().StringVar(&args.llmMode, "llm-mode", "", "platform or native (default platform)")
	cmd.Flags().StringSliceVar(&args.allowedModels, "allowed-model", nil, "Restrict native mode to these vendor model names; repeatable")
	_ = cmd.MarkFlagRequired("runner")
	_ = cmd.MarkFlagRequired("workspace-image")
	_ = cmd.MarkFlagRequired("availability")
	return cmd
}

func newEnvironmentsUpdateCmd() *cobra.Command {
	args := &environmentArgs{}
	cmd := &cobra.Command{
		Use:   "update NAME",
		Short: "Change an environment; flags not given are left as they are",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			organizationID, err := clients.resolveOrganizationID(ctx, args.organizationID)
			if err != nil {
				return err
			}
			environment, err := clients.findEnvironment(ctx, organizationID, positional[0])
			if err != nil {
				return err
			}
			request := &agentsv1.UpdateEnvironmentRequest{Id: environment.GetMeta().GetId()}
			if runner := strings.TrimSpace(args.runner); runner != "" {
				request.RunnerId = &runner
			}
			if flavor := strings.TrimSpace(args.flavor); flavor != "" {
				request.Flavor = &flavor
			}
			if raw := strings.TrimSpace(args.availability); raw != "" {
				availability, err := parseAvailability(raw)
				if err != nil {
					return err
				}
				request.Availability = &availability
			}
			if raw := strings.TrimSpace(args.llmMode); raw != "" {
				mode, err := parseLLMMode(raw)
				if err != nil {
					return err
				}
				request.LlmMode = &mode
			}
			// Sent only when named, so an unrelated update does not silently
			// replace the allowlist with nothing.
			if cmd.Flags().Changed("allowed-model") {
				request.LlmAllowedModels = args.allowedModels
			}
			if raw := strings.TrimSpace(args.workspaceImage); raw != "" {
				imageID, tag, err := splitImageReference(raw)
				if err != nil {
					return fmt.Errorf("--workspace-image: %w", err)
				}
				request.WorkspaceImageId, request.WorkspaceImageTag = &imageID, &tag
			}
			if raw := strings.TrimSpace(args.agentRuntimeImage); raw != "" {
				imageID, tag, err := splitImageReference(raw)
				if err != nil {
					return fmt.Errorf("--agent-runtime-image: %w", err)
				}
				request.AgentRuntimeImageId, request.AgentRuntimeImageTag = &imageID, &tag
			}
			response, err := clients.agents.UpdateEnvironment(ctx, connect.NewRequest(request))
			if err != nil {
				return err
			}
			return output.Print(clients.runContext.OutputFormat, environmentOutputFrom(response.Msg.GetEnvironment()))
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.runner, "runner", "", "Runner ID workloads are placed on")
	cmd.Flags().StringVar(&args.flavor, "flavor", "", "Flavor name in the runner's catalog")
	cmd.Flags().StringVar(&args.workspaceImage, "workspace-image", "", "Workspace image as IMAGE_ID:TAG")
	cmd.Flags().StringVar(&args.agentRuntimeImage, "agent-runtime-image", "", "Agent runtime image as IMAGE_ID:TAG")
	cmd.Flags().StringVar(&args.availability, "availability", "", "internal or private")
	cmd.Flags().StringVar(&args.llmMode, "llm-mode", "", "platform or native")
	cmd.Flags().StringSliceVar(&args.allowedModels, "allowed-model", nil, "Replace the native-mode model allowlist; repeatable")
	return cmd
}

func newEnvironmentsDeleteCmd() *cobra.Command {
	args := &environmentArgs{}
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			organizationID, err := clients.resolveOrganizationID(ctx, args.organizationID)
			if err != nil {
				return err
			}
			environment, err := clients.findEnvironment(ctx, organizationID, positional[0])
			if err != nil {
				return err
			}
			if _, err := clients.agents.DeleteEnvironment(ctx, connect.NewRequest(&agentsv1.DeleteEnvironmentRequest{
				Id: environment.GetMeta().GetId(),
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted environment %s\n", environment.GetName())
			return nil
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

func environmentOutputFrom(environment *agentsv1.Environment) environmentOutput {
	out := environmentOutput{
		ID:           environment.GetMeta().GetId(),
		Name:         environment.GetName(),
		RunnerID:     environment.GetRunnerId(),
		Flavor:       environment.GetFlavor(),
		Availability: availabilityLabel(environment.GetAvailability()),
		LLMMode:      llmModeLabel(environment.GetLlmMode()),
	}
	out.LLMAllowedModels = append([]string(nil), environment.GetLlmAllowedModels()...)
	if id := environment.GetWorkspaceImageId(); id != "" {
		out.WorkspaceImage = id + ":" + environment.GetWorkspaceImageTag()
	}
	if id := environment.GetAgentRuntimeImageId(); id != "" {
		out.AgentRuntimeImage = id + ":" + environment.GetAgentRuntimeImageTag()
	}
	return out
}

func llmModeLabel(mode agentsv1.LLMMode) string {
	if mode == agentsv1.LLMMode_LLM_MODE_NATIVE {
		return "native"
	}
	// Unspecified reads as platform: it is the default the column carries and
	// the mode every environment predating native mode is in.
	return "platform"
}

func parseLLMMode(raw string) (agentsv1.LLMMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "platform":
		return agentsv1.LLMMode_LLM_MODE_PLATFORM, nil
	case "native":
		return agentsv1.LLMMode_LLM_MODE_NATIVE, nil
	default:
		return agentsv1.LLMMode_LLM_MODE_UNSPECIFIED,
			fmt.Errorf("llm-mode must be platform or native, got %q", raw)
	}
}

func availabilityLabel(availability agentsv1.EnvironmentAvailability) string {
	switch availability {
	case agentsv1.EnvironmentAvailability_ENVIRONMENT_AVAILABILITY_INTERNAL:
		return "internal"
	case agentsv1.EnvironmentAvailability_ENVIRONMENT_AVAILABILITY_PRIVATE:
		return "private"
	default:
		return "unspecified"
	}
}

func parseAvailability(raw string) (agentsv1.EnvironmentAvailability, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "internal":
		return agentsv1.EnvironmentAvailability_ENVIRONMENT_AVAILABILITY_INTERNAL, nil
	case "private":
		return agentsv1.EnvironmentAvailability_ENVIRONMENT_AVAILABILITY_PRIVATE, nil
	default:
		return agentsv1.EnvironmentAvailability_ENVIRONMENT_AVAILABILITY_UNSPECIFIED,
			fmt.Errorf("availability must be internal or private, got %q", raw)
	}
}

// splitImageReference reads IMAGE_ID:TAG, the form the environment resource
// stores: a catalog record and a tag within it, never a registry address.
func splitImageReference(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	index := strings.LastIndex(trimmed, ":")
	if index <= 0 || index == len(trimmed)-1 {
		return "", "", fmt.Errorf("expected IMAGE_ID:TAG, got %q", raw)
	}
	return trimmed[:index], trimmed[index+1:], nil
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func (c *sandboxClients) findEnvironment(ctx context.Context, organizationID, name string) (*agentsv1.Environment, error) {
	environments, err := c.listEnvironments(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	for _, environment := range environments {
		if environment.GetName() == name {
			return environment, nil
		}
	}
	return nil, fmt.Errorf("environment %q not found in organization %s", name, organizationID)
}

// isPermissionDenied reports a refusal to read an environment's configuration,
// which `show` states rather than rendering as an empty contents listing.
func isPermissionDenied(err error) bool {
	return connect.CodeOf(err) == connect.CodePermissionDenied
}

func init() {
	rootCmd.AddCommand(newEnvironmentsCmd())
}

func (c *sandboxClients) getEnvironment(ctx context.Context, environmentID string) (*agentsv1.Environment, error) {
	response, err := c.agents.GetEnvironment(ctx, connect.NewRequest(&agentsv1.GetEnvironmentRequest{Id: environmentID}))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetEnvironment(), nil
}
