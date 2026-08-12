package cmd

import (
	"fmt"
	"strings"
	"time"

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

			steps := newSteps(cmd)
			title := "Restoring every platform workload"
			if service != "" {
				title = "Restoring " + service
			}

			// The kubectl output is one line per workload -- forty of them,
			// each saying only that it was replaced, which the step says once
			// with a number. What they were is worth watching while it runs.
			step := steps.Start(title)
			out, err := local.ResetProgress(service, step.Detail)
			if err != nil {
				return step.Fail(err)
			}
			step.Done(fmt.Sprintf("%d workloads", local.CountReplaced(out)))

			if noWait {
				return nil
			}

			// Named rather than counted here: a rollout that stalls is the
			// thing this command is most likely to be run against, so which
			// workload is behind is the whole answer.
			step = steps.Start("Waiting for the rollout")
			var names []string
			if service != "" {
				names = []string{service}
			}
			done := make(chan struct{})
			go func() {
				ticker := time.NewTicker(3 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						if rolling := local.RollingWorkloads(2); len(rolling) > 0 {
							step.Detail("waiting on " + strings.Join(rolling, ", "))
						}
					}
				}
			}()
			err = local.WaitWorkloadsReady(names)
			close(done)
			if err != nil {
				return step.Fail(err)
			}
			step.Done("")
			return nil
		},
	}

	cmd.Flags().StringVar(&service, "service", "", "Restore only this workload (e.g. gateway)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Do not wait for the rollout to finish")

	return cmd
}
