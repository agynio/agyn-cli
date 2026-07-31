package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agynio/agyn-cli/internal/config"
	"github.com/agynio/agyn-cli/internal/local"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newLocalKubeconfigCmd() *cobra.Command {
	var print bool
	var path string

	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Add the VM's cluster to your kubeconfig as its own context",
		Long: "Extracts the k3s kubeconfig from the running VM, points it at the forwarded\n" +
			"API port, and merges it into ~/.kube/config for use with kubectl, helm and\n" +
			"devspace. Each VM gets its own context, since each is a separate cluster.",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if err := local.CheckAPIPort(settings.APIPort); err != nil {
				return fmt.Errorf("%w\n\nThe VM was likely created before API forwarding existed. Recreate it:\n  agyn local delete && agyn local start", err)
			}

			kubeconfig, err := local.FetchKubeconfig(settings.APIPort)
			if err != nil {
				return err
			}

			if print {
				data, err := yaml.Marshal(kubeconfig)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}

			if path != "" {
				data, err := yaml.Marshal(kubeconfig)
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote kubeconfig to %s\n", path)
				return nil
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			target := filepath.Join(home, ".kube", "config")
			if err := local.MergeKubeconfig(target, kubeconfig); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Merged context %q into %s\n", local.KubeContextName(), target)
			fmt.Fprintf(cmd.OutOrStdout(), "Use it with:\n  kubectl config use-context %s\n", local.KubeContextName())
			return nil
		},
	}

	cmd.Flags().BoolVar(&print, "print", false, "Print the kubeconfig to stdout instead of merging")
	cmd.Flags().StringVar(&path, "path", "", "Write a standalone kubeconfig file instead of merging")

	return cmd
}
