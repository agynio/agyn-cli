package local

import (
	"fmt"
	"io"
)

// upgradeScript ships in the image. It upgrades the platform Helm releases in
// place, reusing the values the release already holds.
const upgradeScript = "/opt/agyn/upgrade-platform.sh"

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
func UpgradePlatform(stdout, stderr io.Writer, platformVersion, appsVersion string) error {
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

	args := []string{"shell", InstanceName(), "--", "sudo", upgradeScript}
	// Positional and order-dependent, so an apps version needs a platform slot
	// ahead of it; empty means "latest".
	if platformVersion != "" || appsVersion != "" {
		args = append(args, platformVersion)
	}
	if appsVersion != "" {
		args = append(args, appsVersion)
	}

	if err := limactl(stdout, stderr, args...); err != nil {
		return fmt.Errorf("upgrade the platform in the VM: %w", err)
	}
	return nil
}
