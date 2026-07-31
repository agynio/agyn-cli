package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/local"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/agynio/agyn-cli/internal/terminal"
	"github.com/spf13/cobra"
)

// InstanceSummary is one row of `agyn local list`.
type InstanceSummary struct {
	Name     string `json:"name" yaml:"name"`
	Selected bool   `json:"selected" yaml:"selected"`
	Status   string `json:"status" yaml:"status"`
	Port     int    `json:"port,omitempty" yaml:"port,omitempty"`
	APIPort  int    `json:"apiPort,omitempty" yaml:"apiPort,omitempty"`
	Version  string `json:"version,omitempty" yaml:"version,omitempty"`
	Profile  string `json:"profile" yaml:"profile"`
}

func newLocalListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the local VMs",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			summaries, err := instanceSummaries()
			if err != nil {
				return err
			}
			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, summaries)
			}

			stdout := cmd.OutOrStdout()
			if len(summaries) == 0 {
				fmt.Fprintln(stdout, "No local VMs. Create one with: agyn local start")
				return nil
			}
			fmt.Fprintf(stdout, "%-2s %-16s %-10s %-7s %-7s %-10s %s\n",
				"", "NAME", "STATUS", "PORT", "API", "VERSION", "PROFILE")
			for _, s := range summaries {
				marker := " "
				if s.Selected {
					marker = "*"
				}
				port, apiPort := "-", "-"
				if s.Port != 0 {
					port = strconv.Itoa(s.Port)
				}
				if s.APIPort != 0 {
					apiPort = strconv.Itoa(s.APIPort)
				}
				fmt.Fprintf(stdout, "%-2s %-16s %-10s %-7s %-7s %-10s %s\n",
					marker, s.Name, s.Status, port, apiPort, s.Version, s.Profile)
			}
			return nil
		},
	}
}

func newLocalSelectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "select",
		Short: "Interactively choose the VM other commands act on",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Checked before listing so a script gets the pointer to `use`
			// rather than a prompt nobody is there to answer.
			if !stdinIsTerminal() {
				return fmt.Errorf("no terminal attached; choose a VM without prompting with 'agyn local use NAME'")
			}
			summaries, err := instanceSummaries()
			if err != nil {
				return err
			}
			if len(summaries) == 0 {
				return fmt.Errorf("no local VMs; create one with 'agyn local start'")
			}

			items := make([]terminal.PickItem, 0, len(summaries))
			for _, s := range summaries {
				detail := fmt.Sprintf("%s, port %d", s.Status, s.Port)
				if s.Selected {
					detail += ", current"
				}
				items = append(items, terminal.PickItem{Label: s.Name, Detail: detail, Current: s.Selected})
			}

			choice, err := terminal.Pick(os.Stdin, cmd.OutOrStdout(), "Select a VM:", items)
			if errors.Is(err, terminal.ErrPickCancelled) {
				fmt.Fprintln(cmd.OutOrStdout(), "Unchanged.")
				return nil
			}
			if err != nil {
				return err
			}
			return useInstance(cmd, summaries[choice].Name)
		},
	}
}

func newLocalUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Choose the VM other commands act on, without prompting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// A typo would otherwise select a VM that does not exist and leave
			// every later command reporting it as missing.
			if _, known := cfg.Local.Instances[name]; !known {
				configured := cfg.InstanceNames()
				if len(configured) == 0 {
					return fmt.Errorf("no local VMs; create one with 'agyn local start --instance %s'", name)
				}
				return fmt.Errorf("no local VM named %q; configured: %s", name, strings.Join(configured, ", "))
			}
			return useInstance(cmd, name)
		},
	}
}

func useInstance(cmd *cobra.Command, name string) error {
	if err := config.SaveCurrentInstance(name); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"Selected VM %s; commands use profile %s\n", name, config.LocalProfileFor(name))
	return err
}

// instanceSummaries joins what the config knows about each VM with what Lima
// reports, so a VM configured but never created, or created and since removed,
// is still visible rather than silently absent.
func instanceSummaries() ([]InstanceSummary, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	current := cfg.ResolveInstanceName("")
	names := cfg.InstanceNames()
	summaries := make([]InstanceSummary, 0, len(names))
	for _, name := range names {
		settings := cfg.InstanceSettings(name)
		local.Use(name)
		status := "not created"
		if instance, err := local.GetInstance(); err == nil && instance.Exists {
			status = instance.Status
		}
		summaries = append(summaries, InstanceSummary{
			Name:     name,
			Selected: name == current,
			Status:   status,
			Port:     settings.Port,
			APIPort:  settings.APIPort,
			Version:  settings.Version,
			Profile:  config.LocalProfileFor(name),
		})
	}
	// Leave the process pointing where it was, so listing does not change what
	// a later call in the same invocation acts on.
	local.Use(current)
	return summaries, nil
}
