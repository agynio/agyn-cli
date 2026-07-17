package cmd

import (
	"fmt"
	"os"

	"github.com/agynio/agyn-cli/internal/local"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

func newLocalCACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ca",
		Short: "Manage the local platform CA certificate",
	}

	cmd.AddCommand(newLocalCAShowCmd())
	cmd.AddCommand(newLocalCAExportCmd())
	cmd.AddCommand(newLocalCAInstallCmd())
	cmd.AddCommand(newLocalCAUninstallCmd())

	return cmd
}

func newLocalCAShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the local CA details and trust status",
		RunE: func(cmd *cobra.Command, args []string) error {
			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}

			if _, err := local.EnsureCA(); err != nil {
				return err
			}
			info, err := local.InspectCA()
			if err != nil {
				return err
			}

			if runContext.OutputFormat != output.FormatTable {
				return output.Print(runContext.OutputFormat, info)
			}

			rows := [][]string{
				{"Subject", info.Subject},
				{"Valid from", info.NotBefore},
				{"Valid until", info.NotAfter},
				{"SHA256", info.Fingerprint},
				{"Path", info.Path},
				{"Trusted", fmt.Sprintf("%t", info.Trusted)},
			}
			return output.Table{Headers: []string{"FIELD", "VALUE"}, Rows: rows}.Render(cmd.OutOrStdout())
		},
	}
}

func newLocalCAExportCmd() *cobra.Command {
	var destination string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the local CA certificate to a file (or - for stdout)",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := local.EnsureCA()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read CA: %w", err)
			}

			if destination == "-" {
				_, err := cmd.OutOrStdout().Write(data)
				return err
			}
			if err := os.WriteFile(destination, data, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", destination, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "CA written to %s\n", destination)
			return nil
		},
	}

	cmd.Flags().StringVarP(&destination, "output-file", "f", "agyn-local-ca.pem", "Destination path, or - for stdout")

	return cmd
}

func newLocalCAInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the local CA into the system trust store (asks for sudo)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := local.EnsureCA(); err != nil {
				return err
			}
			if err := local.InstallCA(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "CA installed and trusted. Restart your browser to pick it up.")
			return nil
		},
	}
}

func newLocalCAUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the local CA from the system trust store (asks for sudo)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := local.UninstallCA(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "CA removed from the trust store.")
			return nil
		},
	}
}
