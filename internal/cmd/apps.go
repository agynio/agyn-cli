package cmd

import (
	"fmt"

	"connectrpc.com/connect"
	appsv1 "github.com/agynio/agyn-cli/gen/agynio/api/apps/v1"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type appOutput struct {
	ID          string `json:"id" yaml:"id"`
	Slug        string `json:"slug" yaml:"slug"`
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Icon        string `json:"icon,omitempty" yaml:"icon,omitempty"`
	CreatedAt   string `json:"created_at" yaml:"created_at"`
}

func newAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Manage apps",
	}

	cmd.AddCommand(newAppsRegisterCmd())
	cmd.AddCommand(newAppsGetCmd())
	cmd.AddCommand(newAppsListCmd())
	cmd.AddCommand(newAppsDeleteCmd())

	return cmd
}

func newAppsRegisterCmd() *cobra.Command {
	var slug string
	var name string
	var description string
	var icon string

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register a new app",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			client := gatewayv1connect.NewAppsGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.CreateApp(cmd.Context(), connect.NewRequest(&appsv1.CreateAppRequest{
				Slug:        slug,
				Name:        name,
				Description: description,
				Icon:        icon,
			}))
			if err != nil {
				return err
			}
			app := response.Msg.GetApp()
			outputData, err := appOutputFrom(app)
			if err != nil {
				return err
			}

			return printAppOutput(runContext.OutputFormat, outputData)
		},
	}

	cmd.Flags().StringVar(&slug, "slug", "", "App slug")
	cmd.Flags().StringVar(&name, "name", "", "App name")
	cmd.Flags().StringVar(&description, "description", "", "App description")
	cmd.Flags().StringVar(&icon, "icon", "", "App icon")
	_ = cmd.MarkFlagRequired("slug")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func newAppsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			client := gatewayv1connect.NewAppsGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.GetApp(cmd.Context(), connect.NewRequest(&appsv1.GetAppRequest{Id: args[0]}))
			if err != nil {
				return err
			}

			app := response.Msg.GetApp()
			outputData, err := appOutputFrom(app)
			if err != nil {
				return err
			}
			return printAppOutput(runContext.OutputFormat, outputData)
		},
	}

	return cmd
}

func newAppsListCmd() *cobra.Command {
	var pageSize int32
	var pageToken string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List apps",
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

			client := gatewayv1connect.NewAppsGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.ListApps(cmd.Context(), connect.NewRequest(&appsv1.ListAppsRequest{
				PageSize:  pageSize,
				PageToken: pageToken,
			}))
			if err != nil {
				return err
			}

			apps := response.Msg.GetApps()
			outputs := make([]appOutput, 0, len(apps))
			rows := make([][]string, 0, len(apps))
			for _, app := range apps {
				outputData, err := appOutputFrom(app)
				if err != nil {
					return err
				}
				outputs = append(outputs, outputData)
				rows = append(rows, []string{
					outputData.ID,
					outputData.Slug,
					outputData.Name,
					outputData.Description,
					outputData.CreatedAt,
				})
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "SLUG", "NAME", "DESCRIPTION", "CREATED_AT"},
					Rows:    rows,
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, outputs)
		},
	}

	cmd.Flags().Int32Var(&pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "Page token")

	return cmd
}

func newAppsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			client := gatewayv1connect.NewAppsGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			_, err = client.DeleteApp(cmd.Context(), connect.NewRequest(&appsv1.DeleteAppRequest{Id: args[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted app %s\n", args[0])
			return err
		},
	}

	return cmd
}

func appOutputFrom(app *appsv1.App) (appOutput, error) {
	if app == nil {
		return appOutput{}, fmt.Errorf("app missing from response")
	}
	meta := app.GetMeta()
	if meta == nil {
		return appOutput{}, fmt.Errorf("app meta missing from response")
	}
	return appOutput{
		ID:          meta.GetId(),
		Slug:        app.GetSlug(),
		Name:        app.GetName(),
		Description: app.GetDescription(),
		Icon:        app.GetIcon(),
		CreatedAt:   formatTimestamp(meta.GetCreatedAt()),
	}, nil
}

func printAppOutput(format output.Format, app appOutput) error {
	if format == output.FormatTable {
		table := output.Table{
			Headers: []string{"ID", "SLUG", "NAME", "DESCRIPTION", "CREATED_AT"},
			Rows: [][]string{{
				app.ID,
				app.Slug,
				app.Name,
				app.Description,
				app.CreatedAt,
			}},
		}
		return output.Print(format, table)
	}

	return output.Print(format, app)
}

func init() {
	rootCmd.AddCommand(newAppsCmd())
}
