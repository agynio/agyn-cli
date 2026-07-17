package cmd

import (
	"fmt"
	"strconv"

	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

func newLocalConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage local VM settings",
	}

	cmd.AddCommand(newLocalConfigListCmd())
	cmd.AddCommand(newLocalConfigGetCmd())
	cmd.AddCommand(newLocalConfigSetCmd())

	return cmd
}

func newLocalConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show all local VM settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.ApplyLocalDefaults()

			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, cfg.Local)
			}

			rows := [][]string{
				{"port", strconv.Itoa(cfg.Local.Port)},
				{"api-port", strconv.Itoa(cfg.Local.APIPort)},
				{"version", cfg.Local.Version},
				{"cpus", strconv.Itoa(cfg.Local.CPUs)},
				{"memory", cfg.Local.Memory},
			}
			return output.Table{Headers: []string{"KEY", "VALUE"}, Rows: rows}.Render(cmd.OutOrStdout())
		},
	}
}

func newLocalConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Show one local VM setting (port, version, cpus, memory)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.ApplyLocalDefaults()

			value, err := localConfigValue(cfg.Local, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), value)
			return nil
		},
	}
}

func newLocalConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change one local VM setting (applied on next start)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.ApplyLocalDefaults()

			settings := cfg.Local
			key, value := args[0], args[1]
			switch key {
			case "port":
				port, err := strconv.Atoi(value)
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("port must be a number between 1 and 65535")
				}
				settings.Port = port
			case "api-port":
				port, err := strconv.Atoi(value)
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("api-port must be a number between 1 and 65535")
				}
				settings.APIPort = port
			case "version":
				settings.Version = value
			case "cpus":
				cpus, err := strconv.Atoi(value)
				if err != nil || cpus < 1 {
					return fmt.Errorf("cpus must be a positive number")
				}
				settings.CPUs = cpus
			case "memory":
				settings.Memory = value
			default:
				return fmt.Errorf("unknown key %q (valid: port, api-port, version, cpus, memory)", key)
			}

			if err := config.SaveLocal(settings); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s = %s (applies on next `agyn local start`; restart if the VM is running)\n", key, value)
			return nil
		},
	}
}

func localConfigValue(local config.LocalConfig, key string) (string, error) {
	switch key {
	case "port":
		return strconv.Itoa(local.Port), nil
	case "api-port":
		return strconv.Itoa(local.APIPort), nil
	case "version":
		return local.Version, nil
	case "cpus":
		return strconv.Itoa(local.CPUs), nil
	case "memory":
		return local.Memory, nil
	default:
		return "", fmt.Errorf("unknown key %q (valid: port, api-port, version, cpus, memory)", key)
	}
}
