package cmd

import (
	"fmt"
	"strconv"

	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/local"
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
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))

			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, settings)
			}

			rows := [][]string{
				{"port", strconv.Itoa(settings.Port)},
				{"api-port", strconv.Itoa(settings.APIPort)},
				{"version", settings.Version},
				{"cpus", strconv.Itoa(settings.CPUs)},
				{"memory", settings.Memory},
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
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))

			value, err := localConfigValue(settings, args[0])
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
			settings := resolveInstancePorts(cfg, local.InstanceName(), cfg.InstanceSettings(local.InstanceName()))
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

			if err := config.SaveInstance(local.InstanceName(), settings); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s = %s (applies on next `agyn local start`; restart if the VM is running)\n", key, value)
			return nil
		},
	}
}

func localConfigValue(settings config.LocalInstance, key string) (string, error) {
	switch key {
	case "port":
		return strconv.Itoa(settings.Port), nil
	case "api-port":
		return strconv.Itoa(settings.APIPort), nil
	case "version":
		return settings.Version, nil
	case "cpus":
		return strconv.Itoa(settings.CPUs), nil
	case "memory":
		return settings.Memory, nil
	default:
		return "", fmt.Errorf("unknown key %q (valid: port, api-port, version, cpus, memory)", key)
	}
}
