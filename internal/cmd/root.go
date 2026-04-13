package cmd

import (
	"context"
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
	formatFlag     string
	noColorFlag    bool
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

		outputValue := outputFlag
		outputChanged := cmd.Flags().Changed("output")
		formatChanged := cmd.Flags().Changed("format")
		if formatChanged && outputChanged {
			return fmt.Errorf("--output and --format are mutually exclusive")
		}
		if formatChanged {
			outputValue = formatFlag
		}
		format, err := output.ParseFormat(outputValue)
		if err != nil {
			return err
		}

		target := cfg.ResolveGatewayTarget(gatewayURLFlag)
		var clients *gateway.Clients
		if requiresAuth(cmd) {
			allowMissing := target.UsesZiti || allowMissingToken(cmd)
			token, err := auth.LoadToken(auth.TokenOptions{AllowMissing: allowMissing})
			if err != nil {
				return err
			}
			clients = gateway.NewClients(target.URL, token)
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
	if cmd.Name() == "help" {
		return false
	}
	if cmd.Flags().Changed("help") {
		return false
	}
	if hasHelpArg() {
		return false
	}
	if cmd.Name() == "auth" {
		return false
	}
	if cmd.Name() == "login" && cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
		return false
	}
	return true
}

func hasHelpArg() bool {
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}
func allowMissingToken(cmd *cobra.Command) bool {
	if strings.TrimSpace(os.Getenv(agentIDEnv)) == "" {
		return false
	}
	return strings.HasPrefix(cmd.CommandPath(), "agyn threads")
}

func init() {
	rootCmd.PersistentFlags().StringVar(&gatewayURLFlag, "gateway-url", "", "Gateway base URL")
	rootCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", string(output.FormatTable), "Output format: table, json, or yaml")
	rootCmd.PersistentFlags().StringVar(&formatFlag, "format", string(output.FormatTable), "Output format: table, json, or yaml")
	rootCmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "Disable color output")
}
