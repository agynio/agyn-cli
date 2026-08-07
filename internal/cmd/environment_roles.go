package cmd

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	usersv1 "github.com/agynio/agyn-cli/gen/agynio/api/users/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type environmentRoleArgs struct {
	organizationID string
	role           string
}

func newEnvironmentRolesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "roles", Short: "Who may read, edit or run in the environment"}
	args := &environmentRoleArgs{}

	list := &cobra.Command{
		Use:   "list ENV",
		Short: "List identities holding a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			response, err := clients.agents.ListEnvironmentRoles(cmd.Context(), connect.NewRequest(&agentsv1.ListEnvironmentRolesRequest{
				EnvironmentId: environment.GetMeta().GetId(),
			}))
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(response.Msg.GetAssignments()))
			for _, assignment := range response.Msg.GetAssignments() {
				rows = append(rows, []string{assignment.GetIdentityId(), environmentRoleLabel(assignment.GetRole())})
			}
			return output.Print(clients.runContext.OutputFormat, output.Table{
				Headers: []string{"IDENTITY", "ROLE"},
				Rows:    rows,
			})
		},
	}

	grant := &cobra.Command{
		Use:   "grant ENV @HANDLE",
		Short: "Assign or change a role",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			role, err := parseEnvironmentRole(args.role)
			if err != nil {
				return err
			}
			identityID, err := clients.resolveIdentityHandle(cmd.Context(), positional[1])
			if err != nil {
				return err
			}
			// `user` is not a read grant. A shell in a sandbox started here
			// reaches the environment's secret-backed variables, its egress
			// credentials and the contents of its volumes.
			if role == agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_USER {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Granting `user` on %s lets %s start a sandbox there, which opens an interactive\n"+
						"shell onto the environment's secrets, egress credentials and volume contents.\n",
					environment.GetName(), positional[1])
			}
			if _, err := clients.agents.SetEnvironmentRole(cmd.Context(), connect.NewRequest(&agentsv1.SetEnvironmentRoleRequest{
				EnvironmentId: environment.GetMeta().GetId(),
				IdentityId:    identityID,
				Role:          role,
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Granted %s on %s to %s\n", args.role, environment.GetName(), positional[1])
			return nil
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke ENV @HANDLE",
		Short: "Remove a role",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, environment, err := resolveEnvironmentArg(cmd, args.organizationID, positional[0])
			if err != nil {
				return err
			}
			identityID, err := clients.resolveIdentityHandle(cmd.Context(), positional[1])
			if err != nil {
				return err
			}
			if _, err := clients.agents.RemoveEnvironmentRole(cmd.Context(), connect.NewRequest(&agentsv1.RemoveEnvironmentRoleRequest{
				EnvironmentId: environment.GetMeta().GetId(),
				IdentityId:    identityID,
			})); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s on %s\n", positional[1], environment.GetName())
			return nil
		},
	}

	for _, sub := range []*cobra.Command{list, grant, revoke} {
		sub.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	}
	grant.Flags().StringVar(&args.role, "role", "", "owner, maintainer or user")
	_ = grant.MarkFlagRequired("role")
	cmd.AddCommand(list, grant, revoke)
	return cmd
}

func parseEnvironmentRole(raw string) (agentsv1.EnvironmentRole, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "owner":
		return agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_OWNER, nil
	case "maintainer":
		return agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_MAINTAINER, nil
	case "user":
		return agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_USER, nil
	default:
		return agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_UNSPECIFIED,
			fmt.Errorf("role must be owner, maintainer or user, got %q", raw)
	}
}

func environmentRoleLabel(role agentsv1.EnvironmentRole) string {
	switch role {
	case agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_OWNER:
		return "owner"
	case agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_MAINTAINER:
		return "maintainer"
	case agentsv1.EnvironmentRole_ENVIRONMENT_ROLE_USER:
		return "user"
	default:
		return "unspecified"
	}
}

// resolveIdentityHandle maps @handle onto an identity id, and passes a bare id
// through so a caller holding one need not look it up.
func (c *sandboxClients) resolveIdentityHandle(ctx context.Context, handle string) (string, error) {
	trimmed := strings.TrimSpace(handle)
	if trimmed == "" {
		return "", fmt.Errorf("identity handle is required")
	}
	if !strings.HasPrefix(trimmed, "@") {
		return trimmed, nil
	}
	username := strings.TrimPrefix(trimmed, "@")
	response, err := c.users.SearchUsers(ctx, connect.NewRequest(&usersv1.SearchUsersRequest{
		Prefix: username,
		Limit:  20,
	}))
	if err != nil {
		return "", err
	}
	for _, entry := range response.Msg.GetUsers() {
		if strings.EqualFold(entry.GetUsername(), username) {
			return entry.GetIdentityId(), nil
		}
	}
	return "", fmt.Errorf("no user found for handle %s", trimmed)
}
