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
	"github.com/agynio/agyn-cli/internal/terminal"
	"github.com/spf13/cobra"
)

type RunContext struct {
	Config  *config.Config
	Clients *gateway.Clients
	// ProfileName is the profile this invocation runs under. Endpoint, token,
	// CA and organization all resolve through it, so a command never has to
	// know which cluster it is talking to.
	ProfileName  string
	OutputFormat output.Format
	NoColor      bool
}

type contextKey struct{}

// stdinIsTerminal decides whether a human is there to be prompted. It asks
// whether stdin is a terminal rather than a character device, so `< /dev/null`
// is correctly non-interactive. It is a variable so the non-interactive paths
// can be exercised without a pty.
var stdinIsTerminal = func() bool { return terminal.IsTerminal(os.Stdin) }

var (
	gatewayURLFlag string
	profileFlag    string
	outputFlag     string
	noColorFlag    bool
)

var rootCmd = &cobra.Command{
	Use:     "agyn",
	Short:   "Agyn CLI",
	Version: versionString(),
	// Errors are printed by Execute so a remote shell exit code can be
	// propagated without an accompanying error line.
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		format, err := output.ParseFormat(outputFlag)
		if err != nil {
			return err
		}

		profileName, err := resolveProfileName(cfg, profileFlag)
		if err != nil {
			return err
		}

		var clients *gateway.Clients
		if requiresAuth(cmd, args) {
			clients, err = newGatewayClients(cfg, profileName, allowMissingToken(cmd))
			if err != nil {
				return err
			}
		}

		runContext := &RunContext{
			Config:       cfg,
			Clients:      clients,
			ProfileName:  profileName,
			OutputFormat: format,
			NoColor:      noColorFlag,
		}

		cmd.SetContext(withRunContext(cmd.Context(), runContext))
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// A remote shell's exit code becomes ours, so `agyn sandbox connect`
		// behaves like ssh in scripts.
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			os.Exit(coded.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
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

// OrganizationID resolves the organization a command acts in from local
// settings alone: an explicit flag, then the environment, then the profile's
// selection. An empty result means nothing local chose one — see
// resolveOrganizationID, which then asks the Gateway.
func (r *RunContext) OrganizationID(flag string) string {
	if r == nil || r.Config == nil {
		if id := strings.TrimSpace(flag); id != "" {
			return id
		}
		return strings.TrimSpace(os.Getenv(config.OrganizationEnv))
	}
	return r.Config.ResolveOrganization(r.ProfileName, flag)
}

// resolveProfileName picks the profile for this invocation and rejects a name
// that was asked for but never configured. A typo would otherwise resolve to
// built-in defaults and quietly run against the wrong cluster.
func resolveProfileName(cfg *config.Config, flag string) (string, error) {
	name := cfg.ResolveProfileName(flag)
	namedExplicitly := strings.TrimSpace(flag) != "" || strings.TrimSpace(os.Getenv(config.ProfileEnv)) != ""
	if !namedExplicitly {
		return name, nil
	}
	if _, configured := cfg.Profiles[name]; configured {
		return name, nil
	}
	if configured := cfg.ProfileNames(); len(configured) > 0 {
		return "", fmt.Errorf("profile %q is not configured; available profiles: %s", name, strings.Join(configured, ", "))
	}
	return "", fmt.Errorf("profile %q is not configured; create it with 'agyn profile set %s --gateway-url URL'", name, name)
}

// newGatewayClients builds the one client every command shares, with the
// endpoint, credential and trust anchors the active profile resolves to.
func newGatewayClients(cfg *config.Config, profileName string, allowMissing bool) (*gateway.Clients, error) {
	target := cfg.ResolveGatewayTargetFor(profileName, gatewayURLFlag)

	// AGYN_TOKEN overrides the stored credential so CI can supply one without
	// writing a file; a Ziti endpoint needs none at all, since the sidecar
	// identity authenticates it.
	token := strings.TrimSpace(os.Getenv(config.TokenEnv))
	if token == "" {
		stored, err := auth.LoadTokenFor(profileName, auth.TokenOptions{AllowMissing: target.UsesZiti || allowMissing})
		if err != nil {
			return nil, err
		}
		token = stored
	}

	caFile, err := cfg.ResolveCAFile(profileName)
	if err != nil {
		return nil, err
	}
	return gateway.NewClients(target.URL, token, gateway.Options{CAFile: caFile})
}

func requiresAuth(cmd *cobra.Command, args []string) bool {
	if cmd.Name() == "help" {
		return false
	}
	if cmd.Flags().Changed("help") {
		return false
	}
	if hasHelpArg(args) {
		return false
	}
	if cmd.Name() == "auth" {
		return false
	}
	if cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
		// `login` is not implemented, and `set-token` is how a profile gets its
		// first credential — neither can require one.
		if cmd.Name() == "login" || cmd.Name() == "set-token" {
			return false
		}
	}
	// The sync endpoint runs inside a container, speaks its protocol on stdin
	// and stdout, and never calls the Gateway. Requiring a credential it does
	// not use would make it fail to start wherever none happens to be
	// configured.
	if cmd.Name() == "serve" && cmd.Parent() != nil && cmd.Parent().Name() == "sync" {
		return false
	}
	// `agyn local` manages the local VM and never talks to the gateway.
	if strings.HasPrefix(cmd.CommandPath(), "agyn local") {
		return false
	}
	// `agyn profile` only reads and writes local configuration.
	if strings.HasPrefix(cmd.CommandPath(), "agyn profile") {
		return false
	}
	return true
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func allowMissingToken(cmd *cobra.Command) bool {
	if strings.TrimSpace(os.Getenv(agentIDEnv)) == "" && strings.TrimSpace(os.Getenv(agynIdentityIDEnv)) == "" {
		return false
	}
	return strings.HasPrefix(cmd.CommandPath(), "agyn threads")
}

func init() {
	rootCmd.PersistentFlags().StringVar(&gatewayURLFlag, "gateway-url", "", "Gateway base URL")
	rootCmd.PersistentFlags().StringVar(&profileFlag, "profile", "", "Profile to run under (default: currentProfile, then \"default\")")
	rootCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", string(output.FormatTable), "Output format: table, json, or yaml")
	rootCmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "Disable color output")
}
