package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	llmv1 "github.com/agynio/agyn-cli/gen/agynio/api/llm/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type environmentContentsResult struct {
	Volumes     []*agentsv1.Volume
	Mcps        []*agentsv1.Mcp
	InitScripts []*agentsv1.InitScript
	Vars        []*agentsv1.Env
}

func (c *sandboxClients) environmentContents(ctx context.Context, environmentID string) (*environmentContentsResult, error) {
	volumes, err := c.listEnvironmentVolumes(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	mcps, err := c.agents.ListMcps(ctx, connect.NewRequest(&agentsv1.ListMcpsRequest{
		EnvironmentId: environmentID, PageSize: environmentPageSize,
	}))
	if err != nil {
		return nil, err
	}
	scripts, err := c.agents.ListInitScripts(ctx, connect.NewRequest(&agentsv1.ListInitScriptsRequest{
		EnvironmentId: environmentID, PageSize: environmentPageSize,
	}))
	if err != nil {
		return nil, err
	}
	vars, err := c.agents.ListEnvs(ctx, connect.NewRequest(&agentsv1.ListEnvsRequest{
		EnvironmentId: environmentID, PageSize: environmentPageSize,
	}))
	if err != nil {
		return nil, err
	}
	return &environmentContentsResult{
		Volumes:     volumes,
		Mcps:        mcps.Msg.GetMcps(),
		InitScripts: scripts.Msg.GetInitScripts(),
		Vars:        vars.Msg.GetEnvs(),
	}, nil
}

func (c *sandboxClients) listEnvironmentVolumes(ctx context.Context, environmentID string) ([]*agentsv1.Volume, error) {
	response, err := c.agents.ListVolumes(ctx, connect.NewRequest(&agentsv1.ListVolumesRequest{
		EnvironmentId: environmentID, PageSize: environmentPageSize,
	}))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetVolumes(), nil
}

func printEnvironmentContents(writer io.Writer, contents *environmentContentsResult) error {
	fmt.Fprintf(writer, "\nVolumes:\n")
	if len(contents.Volumes) == 0 {
		fmt.Fprintf(writer, "  (none — nothing written in a workload here survives it stopping)\n")
	}
	for _, volume := range contents.Volumes {
		persistence := "ephemeral"
		if volume.GetPersistent() {
			persistence = volume.GetSize()
		}
		fmt.Fprintf(writer, "  %-16s %-24s %s\n", volume.GetName(), volume.GetMountPath(), persistence)
	}
	fmt.Fprintf(writer, "\nMCP servers:\n")
	for _, mcp := range contents.Mcps {
		shared := ""
		if names := mcp.GetSharedVolumes(); len(names) > 0 {
			shared = " shares:" + strings.Join(names, ",")
		}
		fmt.Fprintf(writer, "  %-16s %s%s\n", mcp.GetName(), mcp.GetCommand(), shared)
	}
	fmt.Fprintf(writer, "\nInit scripts:\n")
	for _, script := range contents.InitScripts {
		fmt.Fprintf(writer, "  %s\n", valueOrDash(script.GetDescription()))
	}
	fmt.Fprintf(writer, "\nVariables:\n")
	for _, variable := range contents.Vars {
		// A secret-backed variable prints as a reference, never a value.
		value := variable.GetValue()
		if variable.GetSecretId() != "" {
			value = "secret:" + variable.GetSecretId()
		}
		fmt.Fprintf(writer, "  %-24s %s\n", variable.GetName(), value)
	}
	return nil
}

// ---------------------------------------------------------------------------
// volumes
// ---------------------------------------------------------------------------

type volumeArgs struct {
	organizationID string
	path           string
	size           string
	storageClass   string
	ttl            string
}

func newEnvironmentVolumesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "volumes", Short: "Volumes the environment declares"}
	cmd.AddCommand(newEnvironmentVolumesListCmd())
	cmd.AddCommand(newEnvironmentVolumesAddCmd())
	cmd.AddCommand(newEnvironmentVolumesRemoveCmd())
	return cmd
}

func newEnvironmentVolumesListCmd() *cobra.Command {
	args := &volumeArgs{}
	cmd := &cobra.Command{
		Use:   "list ENV",
		Short: "List the environment's volumes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			volumes, err := clients.listEnvironmentVolumes(cmd.Context(), environment.GetMeta().GetId())
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(volumes))
			for _, volume := range volumes {
				persistence := "ephemeral"
				if volume.GetPersistent() {
					persistence = "persistent"
				}
				rows = append(rows, []string{
					volume.GetName(), volume.GetMountPath(), persistence,
					valueOrDash(volume.GetSize()), valueOrDash(volume.GetStorageClass()), valueOrDash(volume.GetTtl()),
				})
			}
			return output.Print(clients.runContext.OutputFormat, output.Table{
				Headers: []string{"NAME", "PATH", "PERSISTENCE", "SIZE", "STORAGE_CLASS", "TTL"},
				Rows:    rows,
			})
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

func newEnvironmentVolumesAddCmd() *cobra.Command {
	args := &volumeArgs{}
	cmd := &cobra.Command{
		Use:   "add ENV NAME",
		Short: "Add a volume; --size is what makes it persistent",
		Long: "Given --size, the volume is a disk that survives workload stops. Omitted, it\n" +
			"is ephemeral scratch discarded with the workload. The resource makes the two\n" +
			"biconditional, so there is no separate --persistent flag to contradict it.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			request := &agentsv1.CreateVolumeRequest{
				Target:    &agentsv1.CreateVolumeRequest_EnvironmentId{EnvironmentId: environment.GetMeta().GetId()},
				Name:      positional[1],
				MountPath: strings.TrimSpace(args.path),
			}
			if size := strings.TrimSpace(args.size); size != "" {
				request.Size = size
				request.Persistent = true
			}
			if class := strings.TrimSpace(args.storageClass); class != "" {
				request.StorageClass = &class
			}
			if ttl := strings.TrimSpace(args.ttl); ttl != "" {
				request.Ttl = &ttl
			}
			response, err := clients.agents.CreateVolume(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			return output.Print(clients.runContext.OutputFormat, volumeOutputFrom(response.Msg.GetVolume()))
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.path, "path", "", "Absolute container path for the mount")
	cmd.Flags().StringVar(&args.size, "size", "", "Capacity, e.g. 10Gi. Given, the volume is persistent")
	cmd.Flags().StringVar(&args.storageClass, "storage-class", "", "Storage class in the runner's catalog")
	cmd.Flags().StringVar(&args.ttl, "ttl", "", "Delete an owner's disk this long after its last workload stops")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func newEnvironmentVolumesRemoveCmd() *cobra.Command {
	args := &volumeArgs{}
	cmd := &cobra.Command{
		Use:   "remove ENV NAME",
		Short: "Remove a volume and deprovision every disk made from it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			volumes, err := clients.listEnvironmentVolumes(cmd.Context(), environment.GetMeta().GetId())
			if err != nil {
				return err
			}
			var target *agentsv1.Volume
			for _, volume := range volumes {
				if volume.GetName() == positional[1] {
					target = volume
					break
				}
			}
			if target == nil {
				return fmt.Errorf("environment %q declares no volume %q", environment.GetName(), positional[1])
			}
			// A definition is not a disk: one is provisioned per agent instance
			// and per sandbox, and removing the definition removes all of them.
			if target.GetPersistent() {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Removing %q deprovisions every disk made from it: one per agent instance and per sandbox that has run in %s.\n",
					target.GetName(), environment.GetName())
			}
			if _, err := clients.agents.DeleteVolume(cmd.Context(), connect.NewRequest(&agentsv1.DeleteVolumeRequest{
				Id: target.GetMeta().GetId(),
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed volume %s\n", target.GetName())
			return nil
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

type volumeCLIOutput struct {
	Name         string `json:"name" yaml:"name"`
	MountPath    string `json:"mount_path" yaml:"mount_path"`
	Persistent   bool   `json:"persistent" yaml:"persistent"`
	Size         string `json:"size,omitempty" yaml:"size,omitempty"`
	StorageClass string `json:"storage_class,omitempty" yaml:"storage_class,omitempty"`
	TTL          string `json:"ttl,omitempty" yaml:"ttl,omitempty"`
}

func volumeOutputFrom(volume *agentsv1.Volume) volumeCLIOutput {
	return volumeCLIOutput{
		Name:         volume.GetName(),
		MountPath:    volume.GetMountPath(),
		Persistent:   volume.GetPersistent(),
		Size:         volume.GetSize(),
		StorageClass: volume.GetStorageClass(),
		TTL:          volume.GetTtl(),
	}
}

// ---------------------------------------------------------------------------
// mcps, init scripts, vars
// ---------------------------------------------------------------------------

type mcpArgs struct {
	organizationID string
	image          string
	command        string
	share          []string
}

func newEnvironmentMcpsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mcps", Short: "MCP servers that run in every workload of the environment"}
	args := &mcpArgs{}
	list := &cobra.Command{
		Use:   "list ENV",
		Short: "List the environment's MCP servers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			response, err := clients.agents.ListMcps(cmd.Context(), connect.NewRequest(&agentsv1.ListMcpsRequest{
				EnvironmentId: environment.GetMeta().GetId(), PageSize: environmentPageSize,
			}))
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(response.Msg.GetMcps()))
			for _, mcp := range response.Msg.GetMcps() {
				rows = append(rows, []string{mcp.GetName(), mcp.GetCommand(), strings.Join(mcp.GetSharedVolumes(), ",")})
			}
			return output.Print(clients.runContext.OutputFormat, output.Table{
				Headers: []string{"NAME", "COMMAND", "SHARES"},
				Rows:    rows,
			})
		},
	}
	add := &cobra.Command{
		Use:   "add ENV NAME",
		Short: "Add an MCP server to the environment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			request := &agentsv1.CreateMcpRequest{
				EnvironmentId: environment.GetMeta().GetId(),
				Name:          positional[1],
				Command:       args.command,
				SharedVolumes: args.share,
			}
			if raw := strings.TrimSpace(args.image); raw != "" {
				imageID, tag, err := splitImageReference(raw)
				if err != nil {
					return fmt.Errorf("--image: %w", err)
				}
				request.ImageId, request.ImageTag = imageID, tag
			}
			response, err := clients.agents.CreateMcp(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added MCP server %s\n", response.Msg.GetMcp().GetName())
			return nil
		},
	}
	remove := &cobra.Command{
		Use:   "remove ENV NAME",
		Short: "Remove an MCP server from the environment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			response, err := clients.agents.ListMcps(cmd.Context(), connect.NewRequest(&agentsv1.ListMcpsRequest{
				EnvironmentId: environment.GetMeta().GetId(), PageSize: environmentPageSize,
			}))
			if err != nil {
				return err
			}
			for _, mcp := range response.Msg.GetMcps() {
				if mcp.GetName() != positional[1] {
					continue
				}
				if _, err := clients.agents.DeleteMcp(cmd.Context(), connect.NewRequest(&agentsv1.DeleteMcpRequest{
					Id: mcp.GetMeta().GetId(),
				})); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Removed MCP server %s\n", mcp.GetName())
				return nil
			}
			return fmt.Errorf("environment %q declares no MCP server %q", environment.GetName(), positional[1])
		},
	}
	for _, sub := range []*cobra.Command{list, add, remove} {
		sub.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	}
	add.Flags().StringVar(&args.image, "image", "", "MCP image as IMAGE_ID:TAG")
	add.Flags().StringVar(&args.command, "command", "", "Startup command executed in the container")
	add.Flags().StringArrayVar(&args.share, "share", nil, "Environment volume to mount into this sidecar (repeatable)")
	cmd.AddCommand(list, add, remove)
	return cmd
}

type initScriptArgs struct {
	organizationID string
	file           string
	description    string
}

func newEnvironmentInitScriptsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "init-scripts", Short: "Scripts run before the agent CLI or a sandbox shell"}
	args := &initScriptArgs{}
	list := &cobra.Command{
		Use:   "list ENV",
		Short: "List the environment's init scripts in execution order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			response, err := clients.agents.ListInitScripts(cmd.Context(), connect.NewRequest(&agentsv1.ListInitScriptsRequest{
				EnvironmentId: environment.GetMeta().GetId(), PageSize: environmentPageSize,
			}))
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(response.Msg.GetInitScripts()))
			for _, script := range response.Msg.GetInitScripts() {
				rows = append(rows, []string{script.GetMeta().GetId(), valueOrDash(script.GetDescription())})
			}
			return output.Print(clients.runContext.OutputFormat, output.Table{
				Headers: []string{"ID", "DESCRIPTION"},
				Rows:    rows,
			})
		},
	}
	add := &cobra.Command{
		Use:   "add ENV",
		Short: "Add an init script, read from --file or stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			body, err := readScriptBody(cmd, args.file)
			if err != nil {
				return err
			}
			request := &agentsv1.CreateInitScriptRequest{
				Target:      &agentsv1.CreateInitScriptRequest_EnvironmentId{EnvironmentId: environment.GetMeta().GetId()},
				Script:      body,
				Description: args.description,
			}
			if _, err := clients.agents.CreateInitScript(cmd.Context(), connect.NewRequest(request)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added init script to %s\n", environment.GetName())
			return nil
		},
	}
	remove := &cobra.Command{
		Use:   "remove ENV ID",
		Short: "Remove an init script",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, _, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			if _, err := clients.agents.DeleteInitScript(cmd.Context(), connect.NewRequest(&agentsv1.DeleteInitScriptRequest{
				Id: positional[1],
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed init script %s\n", positional[1])
			return nil
		},
	}
	for _, sub := range []*cobra.Command{list, add, remove} {
		sub.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	}
	add.Flags().StringVar(&args.file, "file", "", "Read the script body from this path instead of stdin")
	add.Flags().StringVar(&args.description, "description", "", "What the script does, shown in logs and the Console")
	cmd.AddCommand(list, add, remove)
	return cmd
}

func readScriptBody(cmd *cobra.Command, path string) (string, error) {
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		body, err := os.ReadFile(trimmed)
		if err != nil {
			return "", err
		}
		return string(body), nil
	}
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", fmt.Errorf("script body is empty; pass --file or pipe it on stdin")
	}
	return string(body), nil
}

type varArgs struct {
	organizationID string
	value          string
	secret         string
}

// vars, not env: `agyn environments env` reads as a tautology, and --env on
// sandbox start already names the environment itself.
func newEnvironmentVarsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "vars", Short: "Variables injected into the main container"}
	args := &varArgs{}
	list := &cobra.Command{
		Use:   "list ENV",
		Short: "List the environment's variables",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			response, err := clients.agents.ListEnvs(cmd.Context(), connect.NewRequest(&agentsv1.ListEnvsRequest{
				EnvironmentId: environment.GetMeta().GetId(), PageSize: environmentPageSize,
			}))
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(response.Msg.GetEnvs()))
			for _, variable := range response.Msg.GetEnvs() {
				// A secret-backed value prints as a reference, never resolved.
				value := variable.GetValue()
				if variable.GetSecretId() != "" {
					value = "secret:" + variable.GetSecretId()
				}
				rows = append(rows, []string{variable.GetName(), value})
			}
			return output.Print(clients.runContext.OutputFormat, output.Table{
				Headers: []string{"NAME", "VALUE"},
				Rows:    rows,
			})
		},
	}
	set := &cobra.Command{
		Use:   "set ENV NAME",
		Short: "Set a variable from --value or --secret",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			value := strings.TrimSpace(args.value)
			secret := strings.TrimSpace(args.secret)
			if (value == "") == (secret == "") {
				return fmt.Errorf("pass exactly one of --value or --secret")
			}
			request := &agentsv1.CreateEnvRequest{
				Target: &agentsv1.CreateEnvRequest_EnvironmentId{EnvironmentId: environment.GetMeta().GetId()},
				Name:   positional[1],
			}
			if value != "" {
				request.Source = &agentsv1.CreateEnvRequest_Value{Value: value}
			} else {
				request.Source = &agentsv1.CreateEnvRequest_SecretId{SecretId: secret}
			}
			if _, err := clients.agents.CreateEnv(cmd.Context(), connect.NewRequest(request)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %s on %s\n", positional[1], environment.GetName())
			return nil
		},
	}
	unset := &cobra.Command{
		Use:   "unset ENV NAME",
		Short: "Remove a variable",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			response, err := clients.agents.ListEnvs(cmd.Context(), connect.NewRequest(&agentsv1.ListEnvsRequest{
				EnvironmentId: environment.GetMeta().GetId(), PageSize: environmentPageSize,
			}))
			if err != nil {
				return err
			}
			for _, variable := range response.Msg.GetEnvs() {
				if variable.GetName() != positional[1] {
					continue
				}
				if _, err := clients.agents.DeleteEnv(cmd.Context(), connect.NewRequest(&agentsv1.DeleteEnvRequest{
					Id: variable.GetMeta().GetId(),
				})); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Unset %s on %s\n", positional[1], environment.GetName())
				return nil
			}
			return fmt.Errorf("environment %q declares no variable %q", environment.GetName(), positional[1])
		},
	}
	for _, sub := range []*cobra.Command{list, set, unset} {
		sub.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	}
	set.Flags().StringVar(&args.value, "value", "", "Plain-text value")
	set.Flags().StringVar(&args.secret, "secret", "", "Secret ID resolved at workload start")
	cmd.AddCommand(list, set, unset)
	return cmd
}

func resolveEnvironmentArg(cmd *cobra.Command, organizationIDFlag, name string) (*sandboxClients, *agentsv1.Environment, error) {
	clients, err := sandboxGatewayClients(cmd)
	if err != nil {
		return nil, nil, err
	}
	ctx := cmd.Context()
	organizationID, err := clients.resolveOrganizationID(ctx, organizationIDFlag)
	if err != nil {
		return nil, nil, err
	}
	environment, err := clients.findEnvironment(ctx, organizationID, name)
	if err != nil {
		return nil, nil, err
	}
	return clients, environment, nil
}

// newEnvironmentSubscriptionsCmd is where a subscription meets an environment.
// The subscription itself is an organization resource -- one is normally shared
// by several environments -- so it is created and deleted from `agyn
// subscriptions`, and only bound here.
func newEnvironmentSubscriptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subscriptions",
		Aliases: []string{"subscription"},
		Short:   "Vendor subscriptions the environment's workloads reach models on",
	}
	args := &varArgs{}
	list := &cobra.Command{
		Use:   "list ENV",
		Short: "List the subscriptions attached to the environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			client, _, err := llmGatewayClient(cmd)
			if err != nil {
				return err
			}
			environmentID := environment.GetMeta().GetId()
			response, err := client.ListSubscriptionAttachments(cmd.Context(), connect.NewRequest(&llmv1.ListSubscriptionAttachmentsRequest{
				OrganizationId: environment.GetOrganizationId(),
				EnvironmentId:  &environmentID,
				PageSize:       subscriptionPageSize,
			}))
			if err != nil {
				return err
			}
			attachments := response.Msg.GetSubscriptionAttachments()
			rows := make([][]string, 0, len(attachments))
			for _, attachment := range attachments {
				rows = append(rows, []string{attachment.GetSubscriptionId(), vendorName(attachment.GetVendor())})
			}
			return output.Print(clients.runContext.OutputFormat, output.Table{
				Headers: []string{"SUBSCRIPTION", "VENDOR"},
				Rows:    rows,
			})
		},
	}
	attach := &cobra.Command{
		Use:   "attach ENV SUBSCRIPTION",
		Short: "Attach a subscription to the environment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			client, _, subscription, err := resolveSubscriptionArg(cmd, environment.GetOrganizationId(), positional[1])
			if err != nil {
				return err
			}
			environmentID := environment.GetMeta().GetId()
			response, err := client.CreateSubscriptionAttachment(cmd.Context(), connect.NewRequest(&llmv1.CreateSubscriptionAttachmentRequest{
				SubscriptionId: subscription.GetMeta().GetId(),
				Target:         &llmv1.CreateSubscriptionAttachmentRequest_EnvironmentId{EnvironmentId: environmentID},
			}))
			if err != nil {
				return err
			}
			return output.Print(clients.runContext.OutputFormat, subscriptionAttachmentOutputFrom(response.Msg.GetSubscriptionAttachment()))
		},
	}
	detach := &cobra.Command{
		Use:   "detach ENV SUBSCRIPTION",
		Short: "Detach a subscription from the environment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			_, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			client, _, subscription, err := resolveSubscriptionArg(cmd, environment.GetOrganizationId(), positional[1])
			if err != nil {
				return err
			}
			environmentID := environment.GetMeta().GetId()
			attachment, err := findSubscriptionAttachment(cmd, client, environment.GetOrganizationId(), subscription.GetMeta().GetId(), nil, &environmentID)
			if err != nil {
				return err
			}
			if _, err := client.DeleteSubscriptionAttachment(cmd.Context(), connect.NewRequest(&llmv1.DeleteSubscriptionAttachmentRequest{
				Id: attachment.GetMeta().GetId(),
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Detached %s from %s\n", positional[1], positional[0])
			return nil
		},
	}
	for _, sub := range []*cobra.Command{list, attach, detach} {
		sub.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
		cmd.AddCommand(sub)
	}
	return cmd
}
