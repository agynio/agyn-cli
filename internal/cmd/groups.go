package cmd

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	groupsv1 "github.com/agynio/agyn-cli/gen/agynio/api/groups/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type groupOutput struct {
	ID             string `json:"id" yaml:"id"`
	OrganizationID string `json:"organization_id" yaml:"organization_id"`
	Name           string `json:"name" yaml:"name"`
	Description    string `json:"description,omitempty" yaml:"description,omitempty"`
	Source         string `json:"source" yaml:"source"`
	ExternalID     string `json:"external_id,omitempty" yaml:"external_id,omitempty"`
	CreatedAt      string `json:"created_at" yaml:"created_at"`
	UpdatedAt      string `json:"updated_at" yaml:"updated_at"`
}

type groupMembershipOutput struct {
	ID         string `json:"id" yaml:"id"`
	GroupID    string `json:"group_id" yaml:"group_id"`
	MemberType string `json:"member_type" yaml:"member_type"`
	MemberID   string `json:"member_id" yaml:"member_id"`
	Source     string `json:"source" yaml:"source"`
	CreatedAt  string `json:"created_at" yaml:"created_at"`
	UpdatedAt  string `json:"updated_at" yaml:"updated_at"`
}

type groupArgs struct {
	organizationID string
	name           string
	description    string
	source         string
	pageSize       int32
	pageToken      string
}

type groupMemberArgs struct {
	groupID        string
	organizationID string
	memberType     string
	memberID       string
	pageSize       int32
	pageToken      string
}

func newGroupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "group", Short: "Manage identity groups"}
	cmd.AddCommand(newGroupCreateCmd())
	cmd.AddCommand(newGroupListCmd())
	cmd.AddCommand(newGroupGetCmd())
	cmd.AddCommand(newGroupUpdateCmd())
	cmd.AddCommand(newGroupDeleteCmd())
	cmd.AddCommand(newGroupMemberCmd())
	return cmd
}

func newGroupCreateCmd() *cobra.Command {
	args := &groupArgs{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a group",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			response, err := client.CreateGroup(cmd.Context(), connect.NewRequest(&groupsv1.CreateGroupRequest{
				OrganizationId: organizationID,
				Name:           args.name,
				Description:    args.description,
				Source:         groupsv1.GroupSource_GROUP_SOURCE_PLATFORM,
			}))
			if err != nil {
				return err
			}
			return printGroup(runContext.OutputFormat, response.Msg.GetGroup())
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.name, "name", "", "Group name")
	cmd.Flags().StringVar(&args.description, "description", "", "Group description")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newGroupListCmd() *cobra.Command {
	args := &groupArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List groups",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := groupsGatewayClient(cmd)
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
			request := &groupsv1.ListGroupsRequest{OrganizationId: organizationID, PageSize: args.pageSize, PageToken: args.pageToken}
			if strings.TrimSpace(args.source) != "" {
				source, err := parseGroupSource(args.source)
				if err != nil {
					return err
				}
				request.Source = &source
			}
			response, err := client.ListGroups(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			outputs := make([]groupOutput, 0, len(response.Msg.GetGroups()))
			rows := make([][]string, 0, len(response.Msg.GetGroups()))
			for _, group := range response.Msg.GetGroups() {
				out, err := groupOutputFrom(group)
				if err != nil {
					return err
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.Name, out.Source, out.UpdatedAt})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"ID", "NAME", "SOURCE", "UPDATED_AT"}, Rows: rows})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.source, "source", "", "Group source: platform or scim")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

func newGroupGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			response, err := client.GetGroup(cmd.Context(), connect.NewRequest(&groupsv1.GetGroupRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			return printGroup(runContext.OutputFormat, response.Msg.GetGroup())
		},
	}
	return cmd
}

func newGroupUpdateCmd() *cobra.Command {
	args := &groupArgs{}
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			request := &groupsv1.UpdateGroupRequest{Id: input[0]}
			if cmd.Flags().Changed("name") {
				request.Name = &args.name
			}
			if cmd.Flags().Changed("description") {
				request.Description = &args.description
			}
			response, err := client.UpdateGroup(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			return printGroup(runContext.OutputFormat, response.Msg.GetGroup())
		},
	}
	cmd.Flags().StringVar(&args.name, "name", "", "Group name")
	cmd.Flags().StringVar(&args.description, "description", "", "Group description")
	return cmd
}

func newGroupDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.DeleteGroup(cmd.Context(), connect.NewRequest(&groupsv1.DeleteGroupRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted group %s\n", input[0])
			return err
		},
	}
	return cmd
}

func newGroupMemberCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "member", Short: "Manage group memberships"}
	cmd.AddCommand(newGroupMemberAddCmd())
	cmd.AddCommand(newGroupMemberRemoveCmd())
	cmd.AddCommand(newGroupMemberListCmd())
	cmd.AddCommand(newGroupMemberGroupsCmd())
	return cmd
}

func newGroupMemberAddCmd() *cobra.Command {
	args := &groupMemberArgs{}
	cmd := &cobra.Command{
		Use:   "add <group-id>",
		Short: "Add a group member",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			memberType, err := parseGroupMemberType(args.memberType)
			if err != nil {
				return err
			}
			response, err := client.AddMember(cmd.Context(), connect.NewRequest(&groupsv1.AddMemberRequest{GroupId: input[0], MemberType: memberType, MemberId: args.memberID, Source: groupsv1.GroupSource_GROUP_SOURCE_PLATFORM}))
			if err != nil {
				return err
			}
			return printGroupMembership(runContext.OutputFormat, response.Msg.GetMembership())
		},
	}
	cmd.Flags().StringVar(&args.memberType, "member-type", "", "Member type: user, agent, or app")
	cmd.Flags().StringVar(&args.memberID, "member-id", "", "Member identity ID")
	_ = cmd.MarkFlagRequired("member-type")
	_ = cmd.MarkFlagRequired("member-id")
	return cmd
}

func newGroupMemberRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <group-id> <member-id>",
		Short: "Remove a group member",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.RemoveMember(cmd.Context(), connect.NewRequest(&groupsv1.RemoveMemberRequest{GroupId: input[0], MemberId: input[1]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Removed member %s from group %s\n", input[1], input[0])
			return err
		},
	}
	return cmd
}

func newGroupMemberListCmd() *cobra.Command {
	args := &groupMemberArgs{}
	cmd := &cobra.Command{
		Use:   "list <group-id>",
		Short: "List group members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			if args.pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			request := &groupsv1.ListMembersRequest{GroupId: input[0], PageSize: args.pageSize, PageToken: args.pageToken}
			if strings.TrimSpace(args.memberType) != "" {
				memberType, err := parseGroupMemberType(args.memberType)
				if err != nil {
					return err
				}
				request.MemberType = &memberType
			}
			response, err := client.ListMembers(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			return printGroupMemberships(runContext.OutputFormat, response.Msg.GetMemberships())
		},
	}
	cmd.Flags().StringVar(&args.memberType, "member-type", "", "Member type: user, agent, or app")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

func newGroupMemberGroupsCmd() *cobra.Command {
	args := &groupMemberArgs{}
	cmd := &cobra.Command{
		Use:   "groups",
		Short: "List groups for a member",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := groupsGatewayClient(cmd)
			if err != nil {
				return err
			}
			if args.pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			memberType, err := parseGroupMemberType(args.memberType)
			if err != nil {
				return err
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			response, err := client.ListMemberGroups(cmd.Context(), connect.NewRequest(&groupsv1.ListMemberGroupsRequest{MemberType: memberType, MemberId: args.memberID, OrganizationId: organizationID, PageSize: args.pageSize, PageToken: args.pageToken}))
			if err != nil {
				return err
			}
			outputs := make([]groupOutput, 0, len(response.Msg.GetGroups()))
			rows := make([][]string, 0, len(response.Msg.GetGroups()))
			for _, group := range response.Msg.GetGroups() {
				out, err := groupOutputFrom(group)
				if err != nil {
					return err
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.Name, out.Source, out.UpdatedAt})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"ID", "NAME", "SOURCE", "UPDATED_AT"}, Rows: rows})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.memberType, "member-type", "", "Member type: user, agent, or app")
	cmd.Flags().StringVar(&args.memberID, "member-id", "", "Member identity ID")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	_ = cmd.MarkFlagRequired("member-type")
	_ = cmd.MarkFlagRequired("member-id")
	return cmd
}

func groupsGatewayClient(cmd *cobra.Command) (gatewayv1connect.GroupsGatewayClient, *RunContext, error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return nil, nil, err
	}
	if runContext.Clients == nil {
		return nil, nil, fmt.Errorf("gateway client unavailable")
	}
	client := gatewayv1connect.NewGroupsGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	return client, runContext, nil
}

func printGroup(format output.Format, group *groupsv1.Group) error {
	out, err := groupOutputFrom(group)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "NAME", "SOURCE", "UPDATED_AT"}, Rows: [][]string{{out.ID, out.Name, out.Source, out.UpdatedAt}}})
	}
	return output.Print(format, out)
}

func printGroupMembership(format output.Format, membership *groupsv1.GroupMembership) error {
	out, err := groupMembershipOutputFrom(membership)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "GROUP_ID", "MEMBER_TYPE", "MEMBER_ID", "SOURCE"}, Rows: [][]string{{out.ID, out.GroupID, out.MemberType, out.MemberID, out.Source}}})
	}
	return output.Print(format, out)
}

func printGroupMemberships(format output.Format, memberships []*groupsv1.GroupMembership) error {
	outputs := make([]groupMembershipOutput, 0, len(memberships))
	rows := make([][]string, 0, len(memberships))
	for _, membership := range memberships {
		out, err := groupMembershipOutputFrom(membership)
		if err != nil {
			return err
		}
		outputs = append(outputs, out)
		rows = append(rows, []string{out.ID, out.GroupID, out.MemberType, out.MemberID, out.Source})
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "GROUP_ID", "MEMBER_TYPE", "MEMBER_ID", "SOURCE"}, Rows: rows})
	}
	return output.Print(format, outputs)
}

func groupOutputFrom(group *groupsv1.Group) (groupOutput, error) {
	if group == nil {
		return groupOutput{}, fmt.Errorf("group missing from response")
	}
	meta := group.GetMeta()
	if meta == nil {
		return groupOutput{}, fmt.Errorf("group meta missing from response")
	}
	return groupOutput{ID: meta.GetId(), OrganizationID: group.GetOrganizationId(), Name: group.GetName(), Description: group.GetDescription(), Source: groupSourceString(group.GetSource()), ExternalID: group.GetExternalId(), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt())}, nil
}

func groupMembershipOutputFrom(membership *groupsv1.GroupMembership) (groupMembershipOutput, error) {
	if membership == nil {
		return groupMembershipOutput{}, fmt.Errorf("group membership missing from response")
	}
	meta := membership.GetMeta()
	if meta == nil {
		return groupMembershipOutput{}, fmt.Errorf("group membership meta missing from response")
	}
	return groupMembershipOutput{ID: meta.GetId(), GroupID: membership.GetGroupId(), MemberType: groupMemberTypeString(membership.GetMemberType()), MemberID: membership.GetMemberId(), Source: groupSourceString(membership.GetSource()), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt())}, nil
}

func parseGroupSource(value string) (groupsv1.GroupSource, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "platform":
		return groupsv1.GroupSource_GROUP_SOURCE_PLATFORM, nil
	case "scim":
		return groupsv1.GroupSource_GROUP_SOURCE_SCIM, nil
	default:
		return groupsv1.GroupSource_GROUP_SOURCE_UNSPECIFIED, fmt.Errorf("source must be platform or scim")
	}
}

func parseGroupMemberType(value string) (groupsv1.GroupMemberType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "user":
		return groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_USER, nil
	case "agent":
		return groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_AGENT, nil
	case "app":
		return groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_APP, nil
	default:
		return groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_UNSPECIFIED, fmt.Errorf("member-type must be user, agent, or app")
	}
}

func groupSourceString(source groupsv1.GroupSource) string {
	switch source {
	case groupsv1.GroupSource_GROUP_SOURCE_PLATFORM:
		return "platform"
	case groupsv1.GroupSource_GROUP_SOURCE_SCIM:
		return "scim"
	case groupsv1.GroupSource_GROUP_SOURCE_UNSPECIFIED:
		return "unspecified"
	default:
		panic("unsupported group source " + source.String())
	}
}

func groupMemberTypeString(memberType groupsv1.GroupMemberType) string {
	switch memberType {
	case groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_USER:
		return "user"
	case groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_AGENT:
		return "agent"
	case groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_APP:
		return "app"
	case groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_UNSPECIFIED:
		return "unspecified"
	default:
		panic("unsupported group member type " + memberType.String())
	}
}

func init() {
	rootCmd.AddCommand(newGroupCmd())
}
