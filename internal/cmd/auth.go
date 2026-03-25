package cmd

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	usersv1 "github.com/agynio/agyn-cli/gen/agynio/api/users/v1"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type apiTokenOutput struct {
	ID        string `json:"id" yaml:"id"`
	Name      string `json:"name" yaml:"name"`
	Token     string `json:"token,omitempty" yaml:"token,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	CreatedAt string `json:"created_at,omitempty" yaml:"created_at,omitempty"`
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authentication commands",
	}

	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthCreateTokenCmd())
	cmd.AddCommand(newAuthListTokensCmd())
	cmd.AddCommand(newAuthRevokeTokenCmd())

	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate with Agyn",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Not yet implemented. Place token in ~/.agyn/credentials")
			return err
		},
	}
}

func newAuthCreateTokenCmd() *cobra.Command {
	var name string
	var expiresAt string

	cmd := &cobra.Command{
		Use:   "create-token",
		Short: "Create a new API token",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			var expiresAtTimestamp *timestamppb.Timestamp
			if expiresAt != "" {
				parsed, err := time.Parse(time.RFC3339, expiresAt)
				if err != nil {
					return fmt.Errorf("parse expires-at: %w", err)
				}
				expiresAtTimestamp = timestamppb.New(parsed)
			}

			client := gatewayv1connect.NewUsersGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.CreateAPIToken(cmd.Context(), connect.NewRequest(&usersv1.CreateAPITokenRequest{
				Name:      name,
				ExpiresAt: expiresAtTimestamp,
			}))
			if err != nil {
				return err
			}
			token := response.Msg.GetToken()
			if token == nil {
				return fmt.Errorf("token missing from response")
			}

			outputData := apiTokenOutput{
				ID:        token.GetId(),
				Name:      token.GetName(),
				Token:     response.Msg.GetPlaintextToken(),
				ExpiresAt: formatTimestamp(token.GetExpiresAt()),
				CreatedAt: formatTimestamp(token.GetCreatedAt()),
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "NAME", "TOKEN", "EXPIRES_AT", "CREATED_AT"},
					Rows: [][]string{{
						outputData.ID,
						outputData.Name,
						outputData.Token,
						outputData.ExpiresAt,
						outputData.CreatedAt,
					}},
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, outputData)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Token name")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "Expiry time (RFC3339)")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func newAuthListTokensCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-tokens",
		Short: "List API tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}

			client := gatewayv1connect.NewUsersGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			response, err := client.ListAPITokens(cmd.Context(), connect.NewRequest(&usersv1.ListAPITokensRequest{}))
			if err != nil {
				return err
			}

			tokens := response.Msg.GetTokens()
			outputs := make([]apiTokenOutput, 0, len(tokens))
			rows := make([][]string, 0, len(tokens))
			for _, token := range tokens {
				if token == nil {
					continue
				}
				outputData := apiTokenOutput{
					ID:        token.GetId(),
					Name:      token.GetName(),
					ExpiresAt: formatTimestamp(token.GetExpiresAt()),
					CreatedAt: formatTimestamp(token.GetCreatedAt()),
				}
				outputs = append(outputs, outputData)
				rows = append(rows, []string{
					outputData.ID,
					outputData.Name,
					outputData.ExpiresAt,
					outputData.CreatedAt,
				})
			}

			if runContext.OutputFormat == output.FormatTable {
				table := output.Table{
					Headers: []string{"ID", "NAME", "EXPIRES_AT", "CREATED_AT"},
					Rows:    rows,
				}
				return output.Print(runContext.OutputFormat, table)
			}

			return output.Print(runContext.OutputFormat, outputs)
		},
	}

	return cmd
}

func newAuthRevokeTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke-token <id>",
		Short: "Revoke an API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			if runContext.Clients == nil {
				return fmt.Errorf("gateway client unavailable")
			}
			client := gatewayv1connect.NewUsersGatewayClient(
				runContext.Clients.HTTPClient,
				runContext.Clients.BaseURL,
				runContext.Clients.ConnectOpts()...,
			)
			_, err = client.RevokeAPIToken(cmd.Context(), connect.NewRequest(&usersv1.RevokeAPITokenRequest{
				TokenId: args[0],
			}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Revoked token %s\n", args[0])
			return err
		},
	}

	return cmd
}
