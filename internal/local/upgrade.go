package local

import (
	_ "embed"
	"fmt"
	"io"
	"strings"
)

// A chart cannot upgrade itself, so this one script stays outside it. It ships
// here rather than baked into the image because the image is what an upgrade
// exists to avoid replacing: a fix to the upgrade path itself would otherwise
// need a rebake to reach a machine that is already running.
//
//go:embed scripts/upgrade-platform.sh
var upgradeScript string

// UpgradePlatform moves the platform releases in the running VM to newer chart
// versions, leaving the VM and its data alone.
//
// Upgrading by replacing the disk image would discard every database, thread
// and workload in the VM. That is the right way to get a clean machine — delete
// then start — but the wrong thing for "upgrade" to mean, because it is not
// reversible and destroys work the user did not ask to lose.
//
// Output is streamed rather than captured: a Helm upgrade that waits for
// rollouts takes minutes, and silence for minutes is indistinguishable from a
// hang.
func UpgradePlatform(stdout, stderr io.Writer, platformVersion string) error {
	instance, err := GetInstance()
	if err != nil {
		return err
	}
	if !instance.Exists {
		return fmt.Errorf("no local VM; create one with 'agyn local start'")
	}
	if instance.Status != "Running" {
		return fmt.Errorf("the local VM is not running (%s); start it with 'agyn local start'", instance.Status)
	}

	// Piped to a shell rather than written into the VM: nothing to install, and
	// no baked copy left behind to drift from this caller. Empty means "latest".
	args := []string{"shell", InstanceName(), "--", "sudo", "bash", "-s", "--"}
	if platformVersion != "" {
		args = append(args, "--platform-version", platformVersion)
	}

	if err := limactlStdin(strings.NewReader(upgradeScript), stdout, stderr, args...); err != nil {
		return fmt.Errorf("upgrade the platform in the VM: %w", err)
	}
	return nil
}
