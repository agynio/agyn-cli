package cmd

import (
	"fmt"

	"github.com/agynio/agyn-cli/internal/local"
	"github.com/spf13/cobra"
)

func newLocalResetCmd() *cobra.Command {
	var service string
	var noWait bool

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Restore platform workloads to the released state",
		Long: "Discards out-of-band modifications (devspace dev patches, manual edits) by\n" +
			"restoring workloads from the agyn-platform Helm release stored in the VM.\n" +
			"Data (databases, files, streams) is untouched.",
		RunE: func(cmd *cobra.Command, args []string) error {
			instance, err := local.GetInstance()
			if err != nil {
				return err
			}
			if !instance.Exists || instance.Status != "Running" {
				return fmt.Errorf("the VM is not running; start it with `agyn local start`")
			}

			if service != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Restoring %q from the agyn-platform release...\n", service)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Restoring all platform workloads from the agyn-platform release...")
			}

			out, err := local.Reset(service)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)

			if !noWait {
				fmt.Fprintln(cmd.OutOrStdout(), "Waiting for rollout...")
				var names []string
				if service != "" {
					names = []string{service}
				}
				if err := local.WaitWorkloadsReady(names); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Done.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&service, "service", "", "Restore only this workload (e.g. gateway)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Do not wait for the rollout to finish")

	return cmd
}
