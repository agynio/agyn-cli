package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

const defaultSandboxPageSize = 100

// startWaitTimeout bounds how long `start`/`connect` wait for the orchestrator
// to bring a workload up before giving up and telling the user to retry.
const startWaitTimeout = 5 * time.Minute

type sandboxOutput struct {
	ID              string `json:"id" yaml:"id"`
	Name            string `json:"name" yaml:"name"`
	OrganizationID  string `json:"organization_id" yaml:"organization_id"`
	EnvironmentID   string `json:"environment_id" yaml:"environment_id"`
	EnvironmentName string `json:"environment_name,omitempty" yaml:"environment_name,omitempty"`
	OwnerID         string `json:"owner_id" yaml:"owner_id"`
	Status          string `json:"status" yaml:"status"`
	IdleTimeout     string `json:"idle_timeout" yaml:"idle_timeout"`
	TTL             string `json:"ttl" yaml:"ttl"`
	WorkloadID      string `json:"workload_id,omitempty" yaml:"workload_id,omitempty"`
	LastSessionAt   string `json:"last_session_at,omitempty" yaml:"last_session_at,omitempty"`
	CreatedAt       string `json:"created_at" yaml:"created_at"`
	Age             string `json:"age" yaml:"age"`
}

type sandboxArgs struct {
	organizationID string
	environment    string
	name           string
	all            bool
	terminated     bool
}

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Start and attach to on-demand workloads",
		Long: "Sandboxes are engineer-launched workloads that run an environment's image " +
			"with its secrets and egress rules, with an interactive shell attached.",
	}
	cmd.AddCommand(newSandboxStartCmd())
	cmd.AddCommand(newSandboxConnectCmd())
	cmd.AddCommand(newSandboxListCmd())
	cmd.AddCommand(newSandboxStopCmd())
	cmd.AddCommand(newSandboxDeleteCmd())
	return cmd
}

func newSandboxStartCmd() *cobra.Command {
	args := &sandboxArgs{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Create a sandbox and attach a shell",
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
			environmentID, err := clients.resolveEnvironmentID(ctx, organizationID, args.environment)
			if err != nil {
				return err
			}

			request := &agentsv1.CreateSandboxRequest{
				OrganizationId: organizationID,
				EnvironmentId:  environmentID,
			}
			if name := strings.TrimSpace(args.name); name != "" {
				request.Name = &name
			}
			response, err := clients.agents.CreateSandbox(ctx, connect.NewRequest(request))
			if err != nil {
				return err
			}
			sandbox := response.Msg.GetSandbox()
			fmt.Fprintf(os.Stderr, "Starting sandbox %s...\n", sandbox.GetName())
			return attachToSandbox(ctx, clients, sandbox)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to your sole organization)")
	cmd.Flags().StringVar(&args.environment, "env", "", "Environment name (defaults to the sole environment)")
	cmd.Flags().StringVar(&args.name, "name", "", "Sandbox name (auto-generated when omitted)")
	return cmd
}

func newSandboxConnectCmd() *cobra.Command {
	args := &sandboxArgs{}
	cmd := &cobra.Command{
		Use:   "connect [NAME]",
		Short: "Attach a shell to an existing sandbox",
		Args:  cobra.MaximumNArgs(1),
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
			var name string
			if len(positional) == 1 {
				name = positional[0]
			}
			sandbox, err := clients.resolveSandbox(ctx, organizationID, name)
			if err != nil {
				return err
			}

			// EnsureSandboxRunning is a no-op when running, a restart when
			// stopped, and a fresh start attempt when failed.
			ensured, err := clients.agents.EnsureSandboxRunning(ctx, connect.NewRequest(&agentsv1.EnsureSandboxRunningRequest{
				Id: sandbox.GetMeta().GetId(),
			}))
			if err != nil {
				return err
			}
			return attachToSandbox(ctx, clients, ensured.Msg.GetSandbox())
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to your sole organization)")
	return cmd
}

func newSandboxListCmd() *cobra.Command {
	args := &sandboxArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sandboxes",
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
			// An empty-but-present owner filter asks for the whole organization;
			// omitting it scopes the list to the caller.
			var ownerID *string
			if args.all {
				empty := ""
				ownerID = &empty
			}
			sandboxes, err := clients.listSandboxes(ctx, organizationID, ownerID, args.terminated)
			if err != nil {
				return err
			}
			return printSandboxes(clients.runContext.OutputFormat, sandboxes)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to your sole organization)")
	cmd.Flags().BoolVar(&args.all, "all", false, "List every sandbox in the organization (organization owners)")
	cmd.Flags().BoolVar(&args.terminated, "terminated", false, "Include terminated sandboxes")
	return cmd
}

func newSandboxStopCmd() *cobra.Command {
	args := &sandboxArgs{}
	cmd := &cobra.Command{
		Use:   "stop [NAME]",
		Short: "Stop the workload, keeping the sandbox and its workspace",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			return runSandboxMutation(cmd, args, positional, func(ctx context.Context, clients *sandboxClients, id string) (*agentsv1.Sandbox, error) {
				response, err := clients.agents.StopSandbox(ctx, connect.NewRequest(&agentsv1.StopSandboxRequest{Id: id}))
				if err != nil {
					return nil, err
				}
				return response.Msg.GetSandbox(), nil
			})
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to your sole organization)")
	return cmd
}

func newSandboxDeleteCmd() *cobra.Command {
	args := &sandboxArgs{}
	cmd := &cobra.Command{
		Use:   "delete [NAME]",
		Short: "Terminate the sandbox and delete its workspace volume",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			return runSandboxMutation(cmd, args, positional, func(ctx context.Context, clients *sandboxClients, id string) (*agentsv1.Sandbox, error) {
				response, err := clients.agents.DeleteSandbox(ctx, connect.NewRequest(&agentsv1.DeleteSandboxRequest{Id: id}))
				if err != nil {
					return nil, err
				}
				return response.Msg.GetSandbox(), nil
			})
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to your sole organization)")
	return cmd
}

func runSandboxMutation(
	cmd *cobra.Command,
	args *sandboxArgs,
	positional []string,
	mutate func(context.Context, *sandboxClients, string) (*agentsv1.Sandbox, error),
) error {
	clients, err := sandboxGatewayClients(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	organizationID, err := clients.resolveOrganizationID(ctx, args.organizationID)
	if err != nil {
		return err
	}
	var name string
	if len(positional) == 1 {
		name = positional[0]
	}
	sandbox, err := clients.resolveSandbox(ctx, organizationID, name)
	if err != nil {
		return err
	}
	updated, err := mutate(ctx, clients, sandbox.GetMeta().GetId())
	if err != nil {
		return err
	}
	return printSandbox(clients.runContext.OutputFormat, updated)
}

func printSandbox(format output.Format, sandbox *agentsv1.Sandbox) error {
	out := sandboxOutputFrom(sandbox)
	if format == output.FormatTable {
		return output.Print(format, output.Table{
			Headers: []string{"NAME", "ENVIRONMENT", "STATUS", "AGE"},
			Rows:    [][]string{{out.Name, out.EnvironmentName, out.Status, out.Age}},
		})
	}
	return output.Print(format, out)
}

func printSandboxes(format output.Format, sandboxes []*agentsv1.Sandbox) error {
	outputs := make([]sandboxOutput, 0, len(sandboxes))
	rows := make([][]string, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		out := sandboxOutputFrom(sandbox)
		outputs = append(outputs, out)
		rows = append(rows, []string{out.Name, out.EnvironmentName, out.Status, out.Age, out.LastSessionAt})
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{
			Headers: []string{"NAME", "ENVIRONMENT", "STATUS", "AGE", "LAST_SESSION"},
			Rows:    rows,
		})
	}
	return output.Print(format, outputs)
}

func sandboxOutputFrom(sandbox *agentsv1.Sandbox) sandboxOutput {
	out := sandboxOutput{
		ID:              sandbox.GetMeta().GetId(),
		Name:            sandbox.GetName(),
		OrganizationID:  sandbox.GetOrganizationId(),
		EnvironmentID:   sandbox.GetEnvironmentId(),
		EnvironmentName: sandbox.GetEnvironmentName(),
		OwnerID:         sandbox.GetOwnerId(),
		Status:          sandboxStatusLabel(sandbox.GetStatus()),
		IdleTimeout:     sandbox.GetIdleTimeout(),
		TTL:             sandbox.GetTtl(),
		WorkloadID:      sandbox.GetWorkloadId(),
	}
	if created := sandbox.GetMeta().GetCreatedAt(); created != nil {
		createdAt := created.AsTime()
		out.CreatedAt = createdAt.Format(time.RFC3339)
		out.Age = humanizeDuration(time.Since(createdAt))
	}
	if last := sandbox.GetLastSessionAt(); last != nil {
		out.LastSessionAt = humanizeDuration(time.Since(last.AsTime())) + " ago"
	}
	return out
}

func sandboxStatusLabel(status agentsv1.SandboxStatus) string {
	switch status {
	case agentsv1.SandboxStatus_SANDBOX_STATUS_STARTING:
		return "starting"
	case agentsv1.SandboxStatus_SANDBOX_STATUS_RUNNING:
		return "running"
	case agentsv1.SandboxStatus_SANDBOX_STATUS_STOPPED:
		return "stopped"
	case agentsv1.SandboxStatus_SANDBOX_STATUS_FAILED:
		return "failed"
	case agentsv1.SandboxStatus_SANDBOX_STATUS_TERMINATED:
		return "terminated"
	default:
		return "unknown"
	}
}

// humanizeDuration renders an age the way kubectl does: one significant unit.
func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func init() {
	rootCmd.AddCommand(newSandboxCmd())
}
