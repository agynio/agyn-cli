package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/gateway"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type RunContext struct {
	Config       *config.Config
	Clients      *gateway.Clients
	OutputFormat output.Format
	NoColor      bool
}

type contextKey struct{}

var (
	gatewayURLFlag string
	outputFlag     string
	noColorFlag    bool
)

const (
	// OpenZiti gateways use .ziti hostnames inside agent pods.
	zitiGatewaySuffix = ".ziti"
)

var rootCmd = &cobra.Command{
	Use:          "agyn",
	Short:        "Agyn CLI",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		format, err := output.ParseFormat(outputFlag)
		if err != nil {
			return err
		}

		var clients *gateway.Clients
		if requiresAuth(cmd) {
			baseURL := cfg.ResolveGatewayURL(gatewayURLFlag)
			token, err := loadAuthToken(baseURL)
			if err != nil {
				return err
			}
			clients = gateway.NewClients(baseURL, token)
		}

		runContext := &RunContext{
			Config:       cfg,
			Clients:      clients,
			OutputFormat: format,
			NoColor:      noColorFlag,
		}

		cmd.SetContext(withRunContext(cmd.Context(), runContext))
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func RunContextFrom(cmd *cobra.Command) (*RunContext, error) {
	ctx := cmd.Context()
	runContext, ok := ctx.Value(contextKey{}).(*RunContext)
	if !ok || runContext == nil {
		return nil, fmt.Errorf("run context unavailable")
	}
	return runContext, nil
}

func withRunContext(ctx context.Context, runContext *RunContext) context.Context {
	return context.WithValue(ctx, contextKey{}, runContext)
}

func requiresAuth(cmd *cobra.Command) bool {
	if cmd.Name() == "auth" {
		return false
	}
	if cmd.Name() == "login" && cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
		return false
	}
	return true
}

func loadAuthToken(baseURL string) (string, error) {
	token, err := auth.LoadToken()
	if err == nil {
		return token, nil
	}
	if allowMissingToken(err, baseURL) {
		return "", nil
	}
	return "", err
}

func allowMissingToken(err error, baseURL string) bool {
	if !errors.Is(err, auth.ErrCredentialsNotFound) {
		return false
	}
	if strings.TrimSpace(os.Getenv(config.GatewayAddressEnv)) != "" {
		return true
	}
	return strings.Contains(strings.ToLower(baseURL), zitiGatewaySuffix)
}

func init() {
	rootCmd.PersistentFlags().StringVar(&gatewayURLFlag, "gateway-url", "", "Gateway base URL")
	rootCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", string(output.FormatTable), "Output format: table, json, or yaml")
	rootCmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "Disable color output")
	rootCmd.AddCommand(newAuthCmd())
	rootCmd.AddCommand(newAppsCmd())
	rootCmd.AddCommand(newAppProxyCmd())
	rootCmd.AddCommand(newMessagesCmd())
	rootCmd.AddCommand(newThreadsCmd())
	rootCmd.AddCommand(newExposeCmd())
}
