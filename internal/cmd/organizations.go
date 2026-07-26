package cmd

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	organizationsv1 "github.com/agynio/agyn-cli/gen/agynio/api/organizations/v1"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type organizationOutput struct {
	ID       string `json:"id" yaml:"id"`
	Name     string `json:"name" yaml:"name"`
	Selected bool   `json:"selected" yaml:"selected"`
}

func newOrganizationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "organizations",
		Short: "Choose the organization commands act in",
		Long: "Most resources are org-scoped and the Gateway takes the organization as an\n" +
			"explicit request parameter. The selection is stored as the active profile's\n" +
			"organization, so it belongs to the cluster it was made against and switching\n" +
			"profiles switches organization with it.",
	}
	cmd.AddCommand(newOrganizationsListCmd())
	cmd.AddCommand(newOrganizationsSelectCmd())
	cmd.AddCommand(newOrganizationsUseCmd())
	return cmd
}

func newOrganizationsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the organizations the caller is a member of",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := organizationsGatewayClient(cmd)
			if err != nil {
				return err
			}
			organizations, err := listAccessibleOrganizations(cmd.Context(), client)
			if err != nil {
				return err
			}

			selected := runContext.OrganizationID("")
			outputs := make([]organizationOutput, 0, len(organizations))
			rows := make([][]string, 0, len(organizations))
			for _, organization := range organizations {
				out := organizationOutput{
					ID:       organization.GetId(),
					Name:     organization.GetName(),
					Selected: organization.GetId() == selected,
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{currentMarker(out.Selected), out.ID, out.Name})
			}

			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{
					Headers: []string{"SELECTED", "ID", "NAME"},
					Rows:    rows,
				})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
}

func newOrganizationsSelectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "select",
		Short: "Interactively choose an organization and store the choice",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Checked before the Gateway call so a script gets the pointer to
			// `use` instead of a round trip followed by a prompt nobody answers.
			if !stdinIsTerminal() {
				return fmt.Errorf("no terminal attached; choose an organization without prompting with 'agyn organizations use NAME'")
			}
			client, runContext, err := organizationsGatewayClient(cmd)
			if err != nil {
				return err
			}
			organizations, err := listAccessibleOrganizations(cmd.Context(), client)
			if err != nil {
				return err
			}
			if len(organizations) == 0 {
				return fmt.Errorf("no accessible organizations")
			}

			stdout := cmd.OutOrStdout()
			selected := runContext.OrganizationID("")
			for index, organization := range organizations {
				fmt.Fprintf(stdout, "  %d) %-40s %s%s\n", index+1, organization.GetName(), organization.GetId(),
					selectedSuffix(organization.GetId() == selected))
			}

			choice, err := promptChoice(cmd, len(organizations))
			if err != nil {
				return err
			}
			return storeOrganization(cmd, runContext, organizations[choice])
		},
	}
}

func newOrganizationsUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Select an organization by name or ID without prompting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, runContext, err := organizationsGatewayClient(cmd)
			if err != nil {
				return err
			}
			organizations, err := listAccessibleOrganizations(cmd.Context(), client)
			if err != nil {
				return err
			}
			organization, err := matchOrganization(organizations, args[0])
			if err != nil {
				return err
			}
			return storeOrganization(cmd, runContext, organization)
		},
	}
}

// matchOrganization resolves a reference against the caller's organizations.
// An ID is tried first: a name that happens to look like another organization's
// ID should not redirect the selection.
func matchOrganization(organizations []*organizationsv1.Organization, reference string) (*organizationsv1.Organization, error) {
	wanted := strings.TrimSpace(reference)
	if wanted == "" {
		return nil, fmt.Errorf("organization name or ID must not be empty")
	}
	for _, organization := range organizations {
		if organization.GetId() == wanted {
			return organization, nil
		}
	}
	var matches []*organizationsv1.Organization
	for _, organization := range organizations {
		if strings.EqualFold(organization.GetName(), wanted) {
			matches = append(matches, organization)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("organization %q not found; available:\n%s", wanted, organizationLines(organizations))
	default:
		return nil, fmt.Errorf("organization name %q is ambiguous; select it by ID:\n%s", wanted, organizationLines(matches))
	}
}

func storeOrganization(cmd *cobra.Command, runContext *RunContext, organization *organizationsv1.Organization) error {
	cfg := runContext.Config
	cfg.SetProfile(runContext.ProfileName, config.Profile{Organization: organization.GetId()})
	// CurrentProfile is passed through untouched: selecting an organization
	// under `--profile staging` must not also switch the machine to staging.
	if err := config.SaveProfiles(cfg.CurrentProfile, cfg.Profiles); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Selected organization %s (%s) for profile %s\n",
		organization.GetName(), organization.GetId(), runContext.ProfileName)
	return err
}

func promptChoice(cmd *cobra.Command, count int) (int, error) {
	reader := bufio.NewReader(cmd.InOrStdin())
	for {
		fmt.Fprintf(cmd.OutOrStdout(), "Select organization [1-%d]: ", count)
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("read selection: %w", err)
		}
		choice, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || choice < 1 || choice > count {
			fmt.Fprintf(cmd.ErrOrStderr(), "enter a number between 1 and %d\n", count)
			continue
		}
		return choice - 1, nil
	}
}

func organizationsGatewayClient(cmd *cobra.Command) (gatewayv1connect.OrganizationsGatewayClient, *RunContext, error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return nil, nil, err
	}
	if runContext.Clients == nil {
		return nil, nil, fmt.Errorf("gateway client unavailable")
	}
	client := gatewayv1connect.NewOrganizationsGatewayClient(
		runContext.Clients.HTTPClient,
		runContext.Clients.BaseURL,
		runContext.Clients.ConnectOpts()...,
	)
	return client, runContext, nil
}

func listAccessibleOrganizations(ctx context.Context, client gatewayv1connect.OrganizationsGatewayClient) ([]*organizationsv1.Organization, error) {
	response, err := client.ListAccessibleOrganizations(ctx, connect.NewRequest(&organizationsv1.ListAccessibleOrganizationsRequest{}))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetOrganizations(), nil
}

// resolveOrganizationID picks the organization a command acts in: an explicit
// --organization-id, then AGYN_ORGANIZATION, then the active profile's
// selection, and only then the caller's sole accessible organization. The last
// step keeps commands typeable on a single-organization cluster; ambiguity is
// reported as a list rather than a guess.
func resolveOrganizationID(ctx context.Context, runContext *RunContext, client gatewayv1connect.OrganizationsGatewayClient, explicit string) (string, error) {
	if id := runContext.OrganizationID(explicit); id != "" {
		return id, nil
	}
	organizations, err := listAccessibleOrganizations(ctx, client)
	if err != nil {
		return "", err
	}
	switch len(organizations) {
	case 0:
		return "", fmt.Errorf("no accessible organizations; pass --organization-id")
	case 1:
		return organizations[0].GetId(), nil
	default:
		return "", fmt.Errorf("multiple organizations available; select one with 'agyn organizations select' or pass --organization-id:\n%s",
			organizationLines(organizations))
	}
}

// organizationIDForCommand is resolveOrganizationID for commands that hold no
// Organizations client of their own. It builds one only when the Gateway
// actually has to be asked.
func organizationIDForCommand(cmd *cobra.Command, explicit string) (string, error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return "", err
	}
	if id := runContext.OrganizationID(explicit); id != "" {
		return id, nil
	}
	client, _, err := organizationsGatewayClient(cmd)
	if err != nil {
		return "", err
	}
	return resolveOrganizationID(cmd.Context(), runContext, client, explicit)
}

func organizationLines(organizations []*organizationsv1.Organization) string {
	lines := make([]string, 0, len(organizations))
	for _, organization := range organizations {
		lines = append(lines, fmt.Sprintf("  %s  %s", organization.GetId(), organization.GetName()))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func selectedSuffix(selected bool) string {
	if selected {
		return "  (selected)"
	}
	return ""
}

func init() {
	rootCmd.AddCommand(newOrganizationsCmd())
}
