package cmd

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	networksv1 "github.com/agynio/agyn-cli/gen/agynio/api/networks/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type networkOutput struct {
	ID                string `json:"id" yaml:"id"`
	OrganizationID    string `json:"organization_id" yaml:"organization_id"`
	Name              string `json:"name" yaml:"name"`
	Description       string `json:"description,omitempty" yaml:"description,omitempty"`
	ProvisioningState string `json:"provisioning_state" yaml:"provisioning_state"`
	CreatedAt         string `json:"created_at" yaml:"created_at"`
	UpdatedAt         string `json:"updated_at" yaml:"updated_at"`
}

type tunnelCredentialOutput struct {
	ID                     string `json:"id" yaml:"id"`
	NetworkID              string `json:"network_id" yaml:"network_id"`
	EnrollmentJWTRevealed  bool   `json:"enrollment_jwt_revealed" yaml:"enrollment_jwt_revealed"`
	EnrollmentJWTExpiresAt string `json:"enrollment_jwt_expires_at" yaml:"enrollment_jwt_expires_at"`
	EnrollmentState        string `json:"enrollment_state" yaml:"enrollment_state"`
	Connectivity           string `json:"connectivity" yaml:"connectivity"`
	ProvisioningState      string `json:"provisioning_state" yaml:"provisioning_state"`
	EnrolledAt             string `json:"enrolled_at,omitempty" yaml:"enrolled_at,omitempty"`
	LastSeenAt             string `json:"last_seen_at,omitempty" yaml:"last_seen_at,omitempty"`
	CreatedAt              string `json:"created_at" yaml:"created_at"`
	UpdatedAt              string `json:"updated_at" yaml:"updated_at"`
	EnrollmentJWT          string `json:"enrollment_jwt,omitempty" yaml:"enrollment_jwt,omitempty"`
}

type privateResourceOutput struct {
	ID                string  `json:"id" yaml:"id"`
	OrganizationID    string  `json:"organization_id" yaml:"organization_id"`
	NetworkID         string  `json:"network_id" yaml:"network_id"`
	Name              string  `json:"name" yaml:"name"`
	Protocol          string  `json:"protocol" yaml:"protocol"`
	TargetHost        string  `json:"target_host" yaml:"target_host"`
	TargetPorts       []int32 `json:"target_ports" yaml:"target_ports"`
	InterceptHost     string  `json:"intercept_host" yaml:"intercept_host"`
	InterceptPorts    []int32 `json:"intercept_ports" yaml:"intercept_ports"`
	ProvisioningState string  `json:"provisioning_state" yaml:"provisioning_state"`
	CreatedAt         string  `json:"created_at" yaml:"created_at"`
	UpdatedAt         string  `json:"updated_at" yaml:"updated_at"`
}

type privateResourceAccessOutput struct {
	ID                string `json:"id" yaml:"id"`
	PrivateResourceID string `json:"private_resource_id" yaml:"private_resource_id"`
	PrincipalType     string `json:"principal_type" yaml:"principal_type"`
	PrincipalID       string `json:"principal_id" yaml:"principal_id"`
	ProvisioningState string `json:"provisioning_state" yaml:"provisioning_state"`
	CreatedAt         string `json:"created_at" yaml:"created_at"`
	UpdatedAt         string `json:"updated_at" yaml:"updated_at"`
}

type networkArgs struct {
	organizationID string
	name           string
	description    string
	pageSize       int32
	pageToken      string
}

type privateResourceArgs struct {
	networkID      string
	organizationID string
	name           string
	protocol       string
	targetHost     string
	targetPorts    []int32
	interceptHost  string
	interceptPorts []int32
	pageSize       int32
	pageToken      string
}

type privateResourceAccessArgs struct {
	privateResourceID string
	networkID         string
	principalType     string
	principalID       string
	pageSize          int32
	pageToken         string
}

func newNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "network", Short: "Manage private networks"}
	cmd.AddCommand(newNetworkCreateCmd())
	cmd.AddCommand(newNetworkListCmd())
	cmd.AddCommand(newNetworkGetCmd())
	cmd.AddCommand(newNetworkUpdateCmd())
	cmd.AddCommand(newNetworkDeleteCmd())
	return cmd
}

func newNetworkCreateCmd() *cobra.Command {
	args := &networkArgs{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a private network",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			response, err := client.CreateNetwork(cmd.Context(), connect.NewRequest(&networksv1.CreateNetworkRequest{
				OrganizationId: organizationID,
				Name:           args.name,
				Description:    args.description,
			}))
			if err != nil {
				return err
			}
			return printNetwork(runContext.OutputFormat, response.Msg.GetNetwork())
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.name, "name", "", "Network name")
	cmd.Flags().StringVar(&args.description, "description", "", "Network description")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newNetworkListCmd() *cobra.Command {
	args := &networkArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List private networks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
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
			response, err := client.ListNetworks(cmd.Context(), connect.NewRequest(&networksv1.ListNetworksRequest{OrganizationId: organizationID, PageSize: args.pageSize, PageToken: args.pageToken}))
			if err != nil {
				return err
			}
			outputs := make([]networkOutput, 0, len(response.Msg.GetNetworks()))
			rows := make([][]string, 0, len(response.Msg.GetNetworks()))
			for _, network := range response.Msg.GetNetworks() {
				out, err := networkOutputFrom(network)
				if err != nil {
					return err
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.Name, out.ProvisioningState, out.UpdatedAt})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"ID", "NAME", "STATE", "UPDATED_AT"}, Rows: rows})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

func newNetworkGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a private network",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			response, err := client.GetNetwork(cmd.Context(), connect.NewRequest(&networksv1.GetNetworkRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			return printNetwork(runContext.OutputFormat, response.Msg.GetNetwork())
		},
	}
	return cmd
}

func newNetworkUpdateCmd() *cobra.Command {
	args := &networkArgs{}
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a private network",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			request := &networksv1.UpdateNetworkRequest{Id: input[0]}
			if cmd.Flags().Changed("name") {
				request.Name = &args.name
			}
			if cmd.Flags().Changed("description") {
				request.Description = &args.description
			}
			response, err := client.UpdateNetwork(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			return printNetwork(runContext.OutputFormat, response.Msg.GetNetwork())
		},
	}
	cmd.Flags().StringVar(&args.name, "name", "", "Network name")
	cmd.Flags().StringVar(&args.description, "description", "", "Network description")
	return cmd
}

func newNetworkDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a private network",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.DeleteNetwork(cmd.Context(), connect.NewRequest(&networksv1.DeleteNetworkRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted network %s\n", input[0])
			return err
		},
	}
	return cmd
}

func newTunnelCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tunnel", Short: "Manage private network tunnel credentials"}
	cmd.AddCommand(newTunnelCreateCmd())
	cmd.AddCommand(newTunnelListCmd())
	cmd.AddCommand(newTunnelGetCmd())
	cmd.AddCommand(newTunnelDeleteCmd())
	return cmd
}

func newTunnelCreateCmd() *cobra.Command {
	var networkID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a tunnel credential",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			response, err := client.CreateTunnelCredential(cmd.Context(), connect.NewRequest(&networksv1.CreateTunnelCredentialRequest{NetworkId: networkID}))
			if err != nil {
				return err
			}
			return printTunnelCredential(runContext.OutputFormat, response.Msg.GetTunnelCredential(), response.Msg.GetEnrollmentJwt())
		},
	}
	cmd.Flags().StringVar(&networkID, "network-id", "", "Network ID")
	_ = cmd.MarkFlagRequired("network-id")
	return cmd
}

func newTunnelListCmd() *cobra.Command {
	var networkID string
	var pageSize int32
	var pageToken string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tunnel credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			if pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			response, err := client.ListTunnelCredentials(cmd.Context(), connect.NewRequest(&networksv1.ListTunnelCredentialsRequest{NetworkId: networkID, PageSize: pageSize, PageToken: pageToken}))
			if err != nil {
				return err
			}
			outputs := make([]tunnelCredentialOutput, 0, len(response.Msg.GetTunnelCredentials()))
			rows := make([][]string, 0, len(response.Msg.GetTunnelCredentials()))
			for _, credential := range response.Msg.GetTunnelCredentials() {
				out, err := tunnelCredentialOutputFrom(credential, "")
				if err != nil {
					return err
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.NetworkID, out.EnrollmentState, out.Connectivity, out.ProvisioningState, out.UpdatedAt})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"ID", "NETWORK_ID", "ENROLLMENT", "CONNECTIVITY", "STATE", "UPDATED_AT"}, Rows: rows})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&networkID, "network-id", "", "Network ID")
	cmd.Flags().Int32Var(&pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "Page token")
	_ = cmd.MarkFlagRequired("network-id")
	return cmd
}

func newTunnelGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a tunnel credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			response, err := client.GetTunnelCredential(cmd.Context(), connect.NewRequest(&networksv1.GetTunnelCredentialRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			return printTunnelCredential(runContext.OutputFormat, response.Msg.GetTunnelCredential(), "")
		},
	}
	return cmd
}

func newTunnelDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a tunnel credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.DeleteTunnelCredential(cmd.Context(), connect.NewRequest(&networksv1.DeleteTunnelCredentialRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted tunnel credential %s\n", input[0])
			return err
		},
	}
	return cmd
}

func newResourceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "resource", Short: "Manage private resources"}
	cmd.AddCommand(newResourceCreateCmd())
	cmd.AddCommand(newResourceListCmd())
	cmd.AddCommand(newResourceGetCmd())
	cmd.AddCommand(newResourceUpdateCmd())
	cmd.AddCommand(newResourceDeleteCmd())
	cmd.AddCommand(newResourceGrantCmd())
	return cmd
}

func newResourceCreateCmd() *cobra.Command {
	args := &privateResourceArgs{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a private resource",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			protocol, err := parsePrivateResourceProtocol(args.protocol)
			if err != nil {
				return err
			}
			response, err := client.CreatePrivateResource(cmd.Context(), connect.NewRequest(&networksv1.CreatePrivateResourceRequest{
				NetworkId:      args.networkID,
				Name:           args.name,
				Protocol:       protocol,
				TargetHost:     args.targetHost,
				TargetPorts:    args.targetPorts,
				InterceptHost:  args.interceptHost,
				InterceptPorts: args.interceptPorts,
			}))
			if err != nil {
				return err
			}
			return printPrivateResource(runContext.OutputFormat, response.Msg.GetPrivateResource())
		},
	}
	bindPrivateResourceFlags(cmd, args)
	_ = cmd.MarkFlagRequired("network-id")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("protocol")
	_ = cmd.MarkFlagRequired("target-host")
	_ = cmd.MarkFlagRequired("intercept-host")
	return cmd
}

func newResourceListCmd() *cobra.Command {
	args := &privateResourceArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List private resources",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			if args.pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			request := &networksv1.ListPrivateResourcesRequest{PageSize: args.pageSize, PageToken: args.pageToken}
			// The organization is a filter here rather than a scope, so the
			// profile's selection is applied when it has one and the listing
			// stays cluster-wide when it does not.
			if organizationID := runContext.OrganizationID(args.organizationID); organizationID != "" {
				request.OrganizationId = &organizationID
			}
			if strings.TrimSpace(args.networkID) != "" {
				request.NetworkId = &args.networkID
			}
			response, err := client.ListPrivateResources(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			outputs := make([]privateResourceOutput, 0, len(response.Msg.GetPrivateResources()))
			rows := make([][]string, 0, len(response.Msg.GetPrivateResources()))
			for _, resource := range response.Msg.GetPrivateResources() {
				out, err := privateResourceOutputFrom(resource)
				if err != nil {
					return err
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.Name, out.Protocol, out.InterceptHost, strings.Join(int32SliceStrings(out.InterceptPorts), ","), out.ProvisioningState})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"ID", "NAME", "PROTOCOL", "INTERCEPT_HOST", "PORTS", "STATE"}, Rows: rows})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.networkID, "network-id", "", "Network ID")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

func newResourceGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a private resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			response, err := client.GetPrivateResource(cmd.Context(), connect.NewRequest(&networksv1.GetPrivateResourceRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			return printPrivateResource(runContext.OutputFormat, response.Msg.GetPrivateResource())
		},
	}
	return cmd
}

func newResourceUpdateCmd() *cobra.Command {
	args := &privateResourceArgs{}
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a private resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			request := &networksv1.UpdatePrivateResourceRequest{Id: input[0]}
			if cmd.Flags().Changed("name") {
				request.Name = &args.name
			}
			if cmd.Flags().Changed("protocol") {
				protocol, err := parsePrivateResourceProtocol(args.protocol)
				if err != nil {
					return err
				}
				request.Protocol = &protocol
			}
			if cmd.Flags().Changed("target-host") {
				request.TargetHost = &args.targetHost
			}
			if cmd.Flags().Changed("target-port") {
				request.TargetPortsUpdate = &networksv1.PortListUpdate{Ports: args.targetPorts}
			}
			if cmd.Flags().Changed("intercept-host") {
				request.InterceptHost = &args.interceptHost
			}
			if cmd.Flags().Changed("intercept-port") {
				request.InterceptPortsUpdate = &networksv1.PortListUpdate{Ports: args.interceptPorts}
			}
			response, err := client.UpdatePrivateResource(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			return printPrivateResource(runContext.OutputFormat, response.Msg.GetPrivateResource())
		},
	}
	bindPrivateResourceFlags(cmd, args)
	return cmd
}

func newResourceDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a private resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.DeletePrivateResource(cmd.Context(), connect.NewRequest(&networksv1.DeletePrivateResourceRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted private resource %s\n", input[0])
			return err
		},
	}
	return cmd
}

func newResourceGrantCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "grant", Short: "Manage private resource grants"}
	cmd.AddCommand(newResourceGrantCreateCmd())
	cmd.AddCommand(newResourceGrantListCmd())
	cmd.AddCommand(newResourceGrantDeleteCmd())
	return cmd
}

func newResourceGrantCreateCmd() *cobra.Command {
	args := &privateResourceAccessArgs{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Grant access to a private resource",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			principalType, err := parsePrincipalType(args.principalType)
			if err != nil {
				return err
			}
			response, err := client.CreatePrivateResourceAccess(cmd.Context(), connect.NewRequest(&networksv1.CreatePrivateResourceAccessRequest{PrivateResourceId: args.privateResourceID, PrincipalType: principalType, PrincipalId: args.principalID}))
			if err != nil {
				return err
			}
			return printPrivateResourceAccess(runContext.OutputFormat, response.Msg.GetPrivateResourceAccess())
		},
	}
	cmd.Flags().StringVar(&args.privateResourceID, "resource-id", "", "Private resource ID")
	cmd.Flags().StringVar(&args.principalType, "principal-type", "", "Principal type: agent, environment, user, app, or group")
	cmd.Flags().StringVar(&args.principalID, "principal-id", "", "Principal ID")
	_ = cmd.MarkFlagRequired("resource-id")
	_ = cmd.MarkFlagRequired("principal-type")
	_ = cmd.MarkFlagRequired("principal-id")
	return cmd
}

func newResourceGrantListCmd() *cobra.Command {
	args := &privateResourceAccessArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List private resource grants",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			if args.pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			request := &networksv1.ListPrivateResourceAccessRequest{PageSize: args.pageSize, PageToken: args.pageToken}
			if strings.TrimSpace(args.privateResourceID) != "" {
				request.PrivateResourceId = &args.privateResourceID
			}
			if strings.TrimSpace(args.networkID) != "" {
				request.NetworkId = &args.networkID
			}
			if strings.TrimSpace(args.principalType) != "" {
				principalType, err := parsePrincipalType(args.principalType)
				if err != nil {
					return err
				}
				request.PrincipalType = &principalType
			}
			if strings.TrimSpace(args.principalID) != "" {
				request.PrincipalId = &args.principalID
			}
			response, err := client.ListPrivateResourceAccess(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			outputs := make([]privateResourceAccessOutput, 0, len(response.Msg.GetPrivateResourceAccess()))
			rows := make([][]string, 0, len(response.Msg.GetPrivateResourceAccess()))
			for _, access := range response.Msg.GetPrivateResourceAccess() {
				out, err := privateResourceAccessOutputFrom(access)
				if err != nil {
					return err
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.PrivateResourceID, out.PrincipalType, out.PrincipalID, out.ProvisioningState})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"ID", "RESOURCE_ID", "PRINCIPAL_TYPE", "PRINCIPAL_ID", "STATE"}, Rows: rows})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.privateResourceID, "resource-id", "", "Private resource ID")
	cmd.Flags().StringVar(&args.networkID, "network-id", "", "Network ID")
	cmd.Flags().StringVar(&args.principalType, "principal-type", "", "Principal type: agent, environment, user, app, or group")
	cmd.Flags().StringVar(&args.principalID, "principal-id", "", "Principal ID")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

func newResourceGrantDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Revoke private resource access",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := networksGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.DeletePrivateResourceAccess(cmd.Context(), connect.NewRequest(&networksv1.DeletePrivateResourceAccessRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted private resource grant %s\n", input[0])
			return err
		},
	}
	return cmd
}

func bindPrivateResourceFlags(cmd *cobra.Command, args *privateResourceArgs) {
	cmd.Flags().StringVar(&args.networkID, "network-id", "", "Network ID")
	cmd.Flags().StringVar(&args.name, "name", "", "Resource name")
	cmd.Flags().StringVar(&args.protocol, "protocol", "", "Protocol: tcp, http, or https")
	cmd.Flags().StringVar(&args.targetHost, "target-host", "", "Private target host")
	cmd.Flags().Int32SliceVar(&args.targetPorts, "target-port", nil, "Private target port; repeat or comma-separate")
	cmd.Flags().StringVar(&args.interceptHost, "intercept-host", "", "Intercept host")
	cmd.Flags().Int32SliceVar(&args.interceptPorts, "intercept-port", nil, "Intercept port; repeat or comma-separate")
}

func networksGatewayClient(cmd *cobra.Command) (gatewayv1connect.NetworksGatewayClient, *RunContext, error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return nil, nil, err
	}
	if runContext.Clients == nil {
		return nil, nil, fmt.Errorf("gateway client unavailable")
	}
	client := gatewayv1connect.NewNetworksGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	return client, runContext, nil
}

func printNetwork(format output.Format, network *networksv1.Network) error {
	out, err := networkOutputFrom(network)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "NAME", "STATE", "UPDATED_AT"}, Rows: [][]string{{out.ID, out.Name, out.ProvisioningState, out.UpdatedAt}}})
	}
	return output.Print(format, out)
}

func printTunnelCredential(format output.Format, credential *networksv1.TunnelCredential, enrollmentJWT string) error {
	out, err := tunnelCredentialOutputFrom(credential, enrollmentJWT)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "NETWORK_ID", "ENROLLMENT", "CONNECTIVITY", "STATE", "JWT"}, Rows: [][]string{{out.ID, out.NetworkID, out.EnrollmentState, out.Connectivity, out.ProvisioningState, out.EnrollmentJWT}}})
	}
	return output.Print(format, out)
}

func printPrivateResource(format output.Format, resource *networksv1.PrivateResource) error {
	out, err := privateResourceOutputFrom(resource)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "NAME", "PROTOCOL", "INTERCEPT_HOST", "PORTS", "STATE"}, Rows: [][]string{{out.ID, out.Name, out.Protocol, out.InterceptHost, strings.Join(int32SliceStrings(out.InterceptPorts), ","), out.ProvisioningState}}})
	}
	return output.Print(format, out)
}

func printPrivateResourceAccess(format output.Format, access *networksv1.PrivateResourceAccess) error {
	out, err := privateResourceAccessOutputFrom(access)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "RESOURCE_ID", "PRINCIPAL_TYPE", "PRINCIPAL_ID", "STATE"}, Rows: [][]string{{out.ID, out.PrivateResourceID, out.PrincipalType, out.PrincipalID, out.ProvisioningState}}})
	}
	return output.Print(format, out)
}

func networkOutputFrom(network *networksv1.Network) (networkOutput, error) {
	if network == nil {
		return networkOutput{}, fmt.Errorf("network missing from response")
	}
	meta := network.GetMeta()
	if meta == nil {
		return networkOutput{}, fmt.Errorf("network meta missing from response")
	}
	return networkOutput{ID: meta.GetId(), OrganizationID: network.GetOrganizationId(), Name: network.GetName(), Description: network.GetDescription(), ProvisioningState: provisioningStateString(network.GetProvisioningState()), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt())}, nil
}

func tunnelCredentialOutputFrom(credential *networksv1.TunnelCredential, enrollmentJWT string) (tunnelCredentialOutput, error) {
	if credential == nil {
		return tunnelCredentialOutput{}, fmt.Errorf("tunnel credential missing from response")
	}
	meta := credential.GetMeta()
	if meta == nil {
		return tunnelCredentialOutput{}, fmt.Errorf("tunnel credential meta missing from response")
	}
	return tunnelCredentialOutput{ID: meta.GetId(), NetworkID: credential.GetNetworkId(), EnrollmentJWTRevealed: credential.GetEnrollmentJwtRevealed(), EnrollmentJWTExpiresAt: formatTimestamp(credential.GetEnrollmentJwtExpiresAt()), EnrollmentState: tunnelEnrollmentStateString(credential.GetEnrollmentState()), Connectivity: tunnelConnectivityString(credential.GetConnectivity()), ProvisioningState: provisioningStateString(credential.GetProvisioningState()), EnrolledAt: formatTimestamp(credential.GetEnrolledAt()), LastSeenAt: formatTimestamp(credential.GetLastSeenAt()), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt()), EnrollmentJWT: enrollmentJWT}, nil
}

func privateResourceOutputFrom(resource *networksv1.PrivateResource) (privateResourceOutput, error) {
	if resource == nil {
		return privateResourceOutput{}, fmt.Errorf("private resource missing from response")
	}
	meta := resource.GetMeta()
	if meta == nil {
		return privateResourceOutput{}, fmt.Errorf("private resource meta missing from response")
	}
	return privateResourceOutput{ID: meta.GetId(), OrganizationID: resource.GetOrganizationId(), NetworkID: resource.GetNetworkId(), Name: resource.GetName(), Protocol: privateResourceProtocolString(resource.GetProtocol()), TargetHost: resource.GetTargetHost(), TargetPorts: resource.GetTargetPorts(), InterceptHost: resource.GetInterceptHost(), InterceptPorts: resource.GetInterceptPorts(), ProvisioningState: provisioningStateString(resource.GetProvisioningState()), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt())}, nil
}

func privateResourceAccessOutputFrom(access *networksv1.PrivateResourceAccess) (privateResourceAccessOutput, error) {
	if access == nil {
		return privateResourceAccessOutput{}, fmt.Errorf("private resource access missing from response")
	}
	meta := access.GetMeta()
	if meta == nil {
		return privateResourceAccessOutput{}, fmt.Errorf("private resource access meta missing from response")
	}
	return privateResourceAccessOutput{ID: meta.GetId(), PrivateResourceID: access.GetPrivateResourceId(), PrincipalType: principalTypeString(access.GetPrincipalType()), PrincipalID: access.GetPrincipalId(), ProvisioningState: provisioningStateString(access.GetProvisioningState()), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt())}, nil
}

func parsePrivateResourceProtocol(value string) (networksv1.PrivateResourceProtocol, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tcp":
		return networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_TCP, nil
	case "http":
		return networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_HTTP, nil
	case "https":
		return networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_HTTPS, nil
	default:
		return networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_UNSPECIFIED, fmt.Errorf("protocol must be tcp, http, or https")
	}
}

func parsePrincipalType(value string) (networksv1.PrivateResourceAccessPrincipalType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "agent":
		return networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_AGENT, nil
	case "user":
		return networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_USER, nil
	case "app":
		return networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_APP, nil
	case "group":
		return networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_GROUP, nil
	case "environment":
		return networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_ENVIRONMENT, nil
	default:
		return networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_UNSPECIFIED, fmt.Errorf("principal-type must be agent, environment, user, app, or group")
	}
}

func privateResourceProtocolString(protocol networksv1.PrivateResourceProtocol) string {
	switch protocol {
	case networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_TCP:
		return "tcp"
	case networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_HTTP:
		return "http"
	case networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_HTTPS:
		return "https"
	case networksv1.PrivateResourceProtocol_PRIVATE_RESOURCE_PROTOCOL_UNSPECIFIED:
		return "unspecified"
	default:
		panic("unsupported private resource protocol " + protocol.String())
	}
}

func principalTypeString(principalType networksv1.PrivateResourceAccessPrincipalType) string {
	switch principalType {
	case networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_AGENT:
		return "agent"
	case networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_USER:
		return "user"
	case networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_APP:
		return "app"
	case networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_GROUP:
		return "group"
	case networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_ENVIRONMENT:
		return "environment"
	case networksv1.PrivateResourceAccessPrincipalType_PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_UNSPECIFIED:
		return "unspecified"
	default:
		// A principal this build does not know is still a real grant. Naming it
		// by its enum beats taking the process down while someone is listing.
		return strings.ToLower(strings.TrimPrefix(principalType.String(), "PRIVATE_RESOURCE_ACCESS_PRINCIPAL_TYPE_"))
	}
}

func provisioningStateString(state networksv1.ProvisioningState) string {
	switch state {
	case networksv1.ProvisioningState_PROVISIONING_STATE_ACTIVE:
		return "active"
	case networksv1.ProvisioningState_PROVISIONING_STATE_FAILED:
		return "failed"
	case networksv1.ProvisioningState_PROVISIONING_STATE_REMOVING:
		return "removing"
	case networksv1.ProvisioningState_PROVISIONING_STATE_UNSPECIFIED:
		return "unspecified"
	default:
		panic("unsupported provisioning state " + state.String())
	}
}

func tunnelEnrollmentStateString(state networksv1.TunnelEnrollmentState) string {
	switch state {
	case networksv1.TunnelEnrollmentState_TUNNEL_ENROLLMENT_STATE_PENDING:
		return "pending"
	case networksv1.TunnelEnrollmentState_TUNNEL_ENROLLMENT_STATE_ENROLLED:
		return "enrolled"
	case networksv1.TunnelEnrollmentState_TUNNEL_ENROLLMENT_STATE_UNSPECIFIED:
		return "unspecified"
	default:
		panic("unsupported tunnel enrollment state " + state.String())
	}
}

func tunnelConnectivityString(connectivity networksv1.TunnelConnectivity) string {
	switch connectivity {
	case networksv1.TunnelConnectivity_TUNNEL_CONNECTIVITY_ONLINE:
		return "online"
	case networksv1.TunnelConnectivity_TUNNEL_CONNECTIVITY_OFFLINE:
		return "offline"
	case networksv1.TunnelConnectivity_TUNNEL_CONNECTIVITY_UNSPECIFIED:
		return "unspecified"
	default:
		panic("unsupported tunnel connectivity " + connectivity.String())
	}
}

func init() {
	rootCmd.AddCommand(newNetworkCmd())
	rootCmd.AddCommand(newTunnelCmd())
	rootCmd.AddCommand(newResourceCmd())
}
