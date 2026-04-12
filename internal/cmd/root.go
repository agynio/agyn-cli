package cmd

import (
	"context"
	"fmt"
	"os"

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
		token, err := auth.LoadToken(auth.TokenOptions{AllowMissing: target.UsesZiti})
		if err != nil {
			return err
		}

		clients := gateway.NewClients(target.URL, token)

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

func init() {
	rootCmd.PersistentFlags().StringVar(&gatewayURLFlag, "gateway-url", "", "Gateway base URL")
	rootCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", string(output.FormatTable), "Output format: table, json, or yaml")
	rootCmd.PersistentFlags().StringVar(&formatFlag, "format", string(output.FormatTable), "Output format: table, json, or yaml")
	rootCmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "Disable color output")
}
