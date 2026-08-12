package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/local"
	"github.com/agynio/agyn-cli/internal/terminal"
	"github.com/spf13/cobra"
)

// localProfileFlags select what `start` and `credentials` provision.
type localProfileFlags struct {
	profile   string
	noProfile bool
}

// addLocalProfileFlags registers the provisioning flags.
//
// --profile deliberately shadows the global flag of the same name: everywhere
// else it names the profile a command runs *under*, and the global flag rejects
// a name nothing has configured yet — which is every name here. On these two
// commands it names the profile being written.
func addLocalProfileFlags(cmd *cobra.Command, flags *localProfileFlags) {
	cmd.Flags().StringVar(&flags.profile, "profile", "",
		"Profile to configure (default: the VM's own, e.g. \""+config.LocalProfileName+"\")")
	cmd.Flags().BoolVar(&flags.noProfile, "no-profile", false,
		"Do not configure a profile or store credentials")
}

// targetProfile names the profile being written. Each VM gets its own, so a
// second VM does not overwrite the first one's endpoint and token; the default
// VM keeps the bare "local" name it always had.
func (f localProfileFlags) targetProfile() string {
	if name := strings.TrimSpace(f.profile); name != "" {
		return name
	}
	return config.LocalProfileFor(local.InstanceName())
}

func newLocalCredentialsCmd() *cobra.Command {
	flags := localProfileFlags{}

	cmd := &cobra.Command{
		Use:   "credentials",
		Short: "Configure a profile from the running VM",
		Long: "Re-runs the credential provisioning `agyn local start` performs: installs a\n" +
			"Gateway bootstrap token in the VM, extracts the CA, reads the organization the\n" +
			"image provisioned, and records all three as a profile. Use it to recover after\n" +
			"a token rotation or a hand-edited configuration.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.noProfile {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to do: --no-profile skips credential provisioning.")
				return nil
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))

			instance, err := local.GetInstance()
			if err != nil {
				return err
			}
			if !instance.Exists || instance.Status != "Running" {
				return fmt.Errorf("the VM is not running; start it with `agyn local start`")
			}

			return provisionLocalProfile(cmd, newSteps(cmd), flags, settings.Port, true, "")
		},
	}

	addLocalProfileFlags(cmd, &flags)

	return cmd
}

// provisionLocalProfile leaves the machine able to run every other `agyn`
// command against the VM: the Gateway gets a credential only this host knows,
// and the profile gets the endpoint, organization and CA that go with it.
//
// recordCA is false when the caller was told to leave certificates alone; the
// profile then trusts whatever the system trust store holds.
//
// organization is the id a caller has already read from the VM — the readiness
// wait reads it, because it is one of the two signals that the platform has
// finished starting. Empty means read it here.
func provisionLocalProfile(cmd *cobra.Command, steps *terminal.Steps, flags localProfileFlags, port int, recordCA bool, organization string) error {
	name := flags.targetProfile()

	token, err := localBootstrapToken(name)
	if err != nil {
		return err
	}

	// Three steps, because these are three things. Two of them change the VM
	// and take as long as the workloads they restart; the third writes a file
	// on this machine. Reporting all of it as "configuring a profile" put
	// minutes of cluster reconfiguration under the name of the fastest part,
	// and left the overlay being moved reading as something a profile does.
	step := steps.Start("Installing the Gateway bootstrap token")
	if _, err := local.SetBootstrapToken(token, step.Detail); err != nil {
		return step.Fail(err)
	}
	step.Done("")

	// The image bakes a default port into the URLs it hands a browser — the
	// OIDC redirects and the media proxy origin. Whenever this host forwards
	// something else, signing in would bounce back to a dead port. On a
	// non-default port this restarts most of the platform, so it says which
	// part it is on.
	step = steps.Start("Pointing the platform at port " + strconv.Itoa(port))
	if _, err := local.SetIngressPort(port, step.Detail); err != nil {
		return step.Fail(err)
	}
	step.Done("")

	step = steps.Start("Configuring profile " + name)
	profile := config.Profile{GatewayURL: local.GatewayURL(port), Organization: organization}
	if recordCA {
		// Extracted afresh rather than reused from the cache: a recreated or
		// upgraded VM can carry a different CA, and a stale one fails every
		// handshake with an error that looks like a network fault.
		if err := local.ExtractCA(); err != nil {
			return step.Fail(err)
		}
		caFile, err := local.CAPath()
		if err != nil {
			return step.Fail(err)
		}
		profile.CAFile = shortenHomePath(caFile)
	}
	if profile.Organization == "" {
		profile.Organization, err = local.OrganizationID()
		if err != nil {
			return step.Fail(err)
		}
	}

	current, err := writeLocalProfile(name, profile, token)
	if err != nil {
		return step.Fail(err)
	}
	step.Done("organization " + profile.Organization)

	if current != name {
		steps.Note(fmt.Sprintf("commands still run under profile %s; switch with: agyn profile use %s", current, name))
	}
	return nil
}

// localBootstrapToken reuses the token the profile already holds and mints one
// only when there is none to reuse. Rotating on every start would restart the
// Gateway for no reason, and would leave a VM the user only restarted needing
// new credentials. A stored credential this CLI did not mint belongs to
// something else, so it is replaced rather than installed into the VM.
func localBootstrapToken(profileName string) (string, error) {
	stored, err := auth.LoadTokenFor(profileName, auth.TokenOptions{AllowMissing: true})
	if err != nil {
		return "", err
	}
	if local.IsBootstrapToken(stored) {
		return strings.TrimSpace(stored), nil
	}
	return local.GenerateBootstrapToken()
}

// writeLocalProfile records the provisioned settings and returns the profile
// commands run under afterwards. A machine that has never chosen one adopts
// this profile, so the acceptance case — boot a VM, run `agyn agents list` with
// no flags — holds. A machine that has chosen keeps its choice: pointing
// someone's CLI at a local VM because they booted one would be worse than
// making them type one command.
func writeLocalProfile(name string, profile config.Profile, token string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	cfg.SetProfile(name, profile)

	current := strings.TrimSpace(cfg.CurrentProfile)
	if current == "" {
		current = name
	}
	if err := config.SaveProfiles(current, cfg.Profiles); err != nil {
		return "", err
	}
	// Stored after the settings: a credential recorded for a profile that
	// failed to write would point at nothing.
	if err := auth.SaveTokenFor(name, token); err != nil {
		return "", err
	}
	return current, nil
}

// forgetLocalProfile drops what provisioning wrote. A purge is meant to be a
// clean sweep, and a profile addressing a VM that no longer exists — with a
// token nobody can rotate — is not part of one.
func forgetLocalProfile(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	name := config.LocalProfileFor(local.InstanceName())
	_, configured := cfg.Profiles[name]
	if !configured && !auth.HasTokenFor(name) {
		return nil
	}
	if err := config.RemoveProfile(name); err != nil {
		return err
	}
	if err := auth.RemoveTokenFor(name); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed the %s profile and its stored token.\n", name)
	return nil
}

// shortenHomePath writes paths under the home directory in ~ form. The config
// file is read by eye and copied between machines; an absolute path with
// somebody's username in it survives neither well.
func shortenHomePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return "~/" + filepath.ToSlash(rel)
}
