package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/agynio/agyn-cli/internal/terminal"
	"github.com/spf13/cobra"
)

// profileOutput reports a profile's resolved settings. It carries whether a
// token is stored rather than the token itself: `show` and `list` are the
// commands people paste into issues.
type profileOutput struct {
	Name         string `json:"name" yaml:"name"`
	Current      bool   `json:"current" yaml:"current"`
	GatewayURL   string `json:"gateway_url" yaml:"gateway_url"`
	Organization string `json:"organization,omitempty" yaml:"organization,omitempty"`
	CAFile       string `json:"ca_file,omitempty" yaml:"ca_file,omitempty"`
	TokenStored  bool   `json:"token_stored" yaml:"token_stored"`
}

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage connection profiles",
		Long: "A profile is a named set of connection settings — which Gateway to talk to,\n" +
			"which organization to operate in, and which CA to trust. Profiles let one\n" +
			"machine address a local VM and one or more remote clusters without rewriting\n" +
			"configuration between commands.",
	}
	cmd.AddCommand(newProfileListCmd())
	cmd.AddCommand(newProfileShowCmd())
	cmd.AddCommand(newProfileTokenCmd())
	cmd.AddCommand(newProfileSelectCmd())
	cmd.AddCommand(newProfileUseCmd())
	cmd.AddCommand(newProfileSetCmd())
	cmd.AddCommand(newProfileRemoveCmd())
	return cmd
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			cfg := runContext.Config

			names := cfg.ProfileNames()
			// The active profile may resolve entirely from built-in defaults on
			// a machine that has never written configuration. Listing it anyway
			// beats printing nothing while commands are demonstrably running
			// under something.
			if !containsString(names, runContext.ProfileName) {
				names = append(names, runContext.ProfileName)
			}

			outputs := make([]profileOutput, 0, len(names))
			rows := make([][]string, 0, len(names))
			for _, name := range names {
				out := profileOutputFor(cfg, name, runContext.ProfileName)
				outputs = append(outputs, out)
				rows = append(rows, []string{
					currentMarker(out.Current),
					out.Name,
					out.GatewayURL,
					out.Organization,
					yesNo(out.TokenStored),
				})
			}

			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{
					Headers: []string{"CURRENT", "NAME", "GATEWAY_URL", "ORGANIZATION", "TOKEN"},
					Rows:    rows,
				})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
}

func newProfileShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [NAME]",
		Short: "Show a profile's resolved settings",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			name := runContext.ProfileName
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
				if err := requireProfile(runContext.Config, name); err != nil {
					return err
				}
			}

			out := profileOutputFor(runContext.Config, name, runContext.ProfileName)
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{
					Headers: []string{"NAME", "CURRENT", "GATEWAY_URL", "ORGANIZATION", "CA_FILE", "TOKEN"},
					Rows: [][]string{{
						out.Name,
						yesNo(out.Current),
						out.GatewayURL,
						out.Organization,
						out.CAFile,
						yesNo(out.TokenStored),
					}},
				})
			}
			return output.Print(runContext.OutputFormat, out)
		},
	}
}

// newProfileTokenCmd is the one command that prints a stored token. `show` and
// `list` deliberately report only that one exists, because they are what people
// paste into issues — but something has to be able to read the value, or every
// caller that needs it parses the credentials file and turns its layout into an
// accidental API. That is what the e2e runner did: it needs AGYN_API_TOKEN in
// the environment, the local VM generates that token per install, and the only
// way to reach it was an awk script over ~/.agyn/credentials.
//
// The token is printed bare and alone so it composes:
//
//	AGYN_API_TOKEN="$(agyn profile token)"
func newProfileTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "token [NAME]",
		Short: "Print the token stored for a profile",
		Long: "Writes the token and nothing else to stdout, for use in a command\n" +
			"substitution. Nothing else prints a token: treat the output as the\n" +
			"secret it is, and mask it in CI logs.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			name := runContext.ProfileName
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
				if err := requireProfile(runContext.Config, name); err != nil {
					return err
				}
			}

			// AllowMissing stays false: a caller substituting this into a
			// variable wants a failure, not an empty string that fails later as
			// an unauthenticated request.
			token, err := auth.LoadTokenFor(name, auth.TokenOptions{})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
			return err
		},
	}
}

func newProfileUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Set the profile subsequent commands run under",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[0])
			if err := requireProfile(runContext.Config, name); err != nil {
				return err
			}
			if err := config.SaveProfiles(name, runContext.Config.Profiles); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Now using profile %s\n", name)
			return err
		},
	}
}

func newProfileSelectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "select",
		Short: "Interactively choose the profile subsequent commands run under",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Checked first so a script is pointed at `use` rather than left
			// waiting on a prompt nobody will answer.
			if !stdinIsTerminal() {
				return fmt.Errorf("no terminal attached; choose a profile without prompting with 'agyn profile use NAME'")
			}
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			names := runContext.Config.ProfileNames()
			if len(names) == 0 {
				return fmt.Errorf("no profiles configured; create one with 'agyn profile set NAME --gateway-url URL'")
			}

			current := runContext.Config.ResolveProfileName("")
			items := make([]terminal.PickItem, 0, len(names))
			for _, name := range names {
				profile := runContext.Config.Profiles[name]
				detail := profile.GatewayURL
				if name == current {
					detail += "  (current)"
				}
				items = append(items, terminal.PickItem{Label: name, Detail: detail, Current: name == current})
			}

			choice, err := terminal.Pick(os.Stdin, cmd.OutOrStdout(), "Select a profile:", items)
			if errors.Is(err, terminal.ErrPickCancelled) {
				fmt.Fprintln(cmd.OutOrStdout(), "Unchanged.")
				return nil
			}
			if err != nil {
				return err
			}

			name := names[choice]
			if err := config.SaveProfiles(name, runContext.Config.Profiles); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Now using profile %s\n", name)
			return err
		},
	}
}

func newProfileSetCmd() *cobra.Command {
	var update config.Profile

	cmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Create or update a profile",
		Long: "Fields that are not given are left as they are, so a profile can be built up\n" +
			"one setting at a time.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("profile name must not be empty")
			}
			if update == (config.Profile{}) {
				return fmt.Errorf("nothing to set; pass --gateway-url, --organization or --ca-file")
			}

			cfg := runContext.Config
			cfg.SetProfile(name, update)
			if err := config.SaveProfiles(cfg.CurrentProfile, cfg.Profiles); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Updated profile %s\n", name)
			return err
		},
	}

	cmd.Flags().StringVar(&update.GatewayURL, "gateway-url", "", "Gateway base URL")
	cmd.Flags().StringVar(&update.Organization, "organization", "", "Organization ID for org-scoped commands")
	cmd.Flags().StringVar(&update.CAFile, "ca-file", "", "PEM bundle trusted in addition to the system trust store")

	return cmd
}

func newProfileRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove NAME",
		Short: "Delete a profile and its stored token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[0])
			if err := requireProfile(runContext.Config, name); err != nil {
				return err
			}
			if err := config.RemoveProfile(name); err != nil {
				return err
			}
			// The token outlives the settings otherwise, and a credential for a
			// cluster the user has stopped addressing is one nobody rotates.
			if err := auth.RemoveTokenFor(name); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Removed profile %s\n", name)
			return err
		},
	}
}

func profileOutputFor(cfg *config.Config, name, currentName string) profileOutput {
	profile := cfg.Profile(name)
	return profileOutput{
		Name:         name,
		Current:      name == currentName,
		GatewayURL:   cfg.ResolveGatewayTargetFor(name, "").URL,
		Organization: profile.Organization,
		CAFile:       profile.CAFile,
		TokenStored:  auth.HasTokenFor(name),
	}
}

// requireProfile rejects a name nothing has configured, listing what is
// available — the alternative is a command that silently succeeds against
// built-in defaults.
func requireProfile(cfg *config.Config, name string) error {
	if _, configured := cfg.Profiles[name]; configured {
		return nil
	}
	if names := cfg.ProfileNames(); len(names) > 0 {
		return fmt.Errorf("profile %q is not configured; available profiles: %s", name, strings.Join(names, ", "))
	}
	return fmt.Errorf("profile %q is not configured; create it with 'agyn profile set %s --gateway-url URL'", name, name)
}

func currentMarker(current bool) string {
	if current {
		return "*"
	}
	return ""
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(newProfileCmd())
}
