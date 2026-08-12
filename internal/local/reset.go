package local

import (
	"fmt"
	"strings"
)

// Reset restores platform workloads to the state recorded in the agyn-platform
// Helm release, discarding out-of-band modifications (devspace dev patches,
// manual kubectl edits). Only workload kinds are replaced: they are what dev
// tooling patches, and replacing Services would trip on immutable fields.
//
// A plain `helm upgrade` cannot do this: Helm's three-way merge preserves live
// changes to fields the chart itself did not change between revisions. The
// stored release manifest piped through `kubectl replace` stomps the drift.
//
// service filters the restore to workloads with that metadata.name ("" = all).
// Everything runs inside the VM (helm, kubectl, and the release state live
// there); python3 is guaranteed by cloud-init. Returns the kubectl output.
func Reset(service string) (string, error) {
	return ResetProgress(service, nil)
}

// ResetProgress restores as Reset does, calling onDetail with each workload as
// it is replaced.
func ResetProgress(service string, onDetail func(string)) (string, error) {
	// The python filter is written via a quoted heredoc to dodge nested
	// Go/shell/python quoting; the service filter arrives via AGYN_RESET_SERVICE.
	script := `
set -eu
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
cat >/tmp/agyn-reset-filter.py <<'PYEOF'
import os, sys, yaml

service = os.environ.get("AGYN_RESET_SERVICE", "")
keep = []
for doc in yaml.safe_load_all(sys.stdin.read()):
    if not doc or doc.get("kind") not in ("Deployment", "StatefulSet", "DaemonSet"):
        continue
    if service and doc.get("metadata", {}).get("name") != service:
        continue
    keep.append(doc)
if not keep:
    sys.exit(3)
print(yaml.dump_all(keep))
PYEOF
helm get manifest agyn-platform -n agyn-platform | python3 /tmp/agyn-reset-filter.py >/tmp/agyn-reset.yaml || {
  status=$?
  if [ "${status}" -eq 3 ]; then
    echo "no matching workloads in the agyn-platform release" >&2
  fi
  exit "${status}"
}
kubectl replace -n agyn-platform -f /tmp/agyn-reset.yaml
rm -f /tmp/agyn-reset.yaml /tmp/agyn-reset-filter.py
`

	out, err := Shell("sudo", "env", "AGYN_RESET_SERVICE="+service, "sh", "-c", script)
	if err != nil {
		return "", err
	}
	if onDetail != nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if name, ok := strings.CutSuffix(strings.TrimSpace(line), " replaced"); ok {
				onDetail(name)
			}
		}
	}
	return strings.TrimSpace(out), nil
}

// CountReplaced is how many workloads a reset restored, for a step to report
// instead of naming all forty.
func CountReplaced(out string) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), " replaced") {
			count++
		}
	}
	return count
}

// WaitWorkloadsReady blocks until the given deployments (or all in the
// platform namespace when names is empty) finish rolling out.
func WaitWorkloadsReady(names []string) error {
	target := "deploy"
	if len(names) > 0 {
		target = "deploy/" + strings.Join(names, " deploy/")
	}
	script := fmt.Sprintf(`
set -eu
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
for t in %s; do
  kubectl -n agyn-platform rollout status "$t" --timeout=300s
done
`, target)
	if len(names) == 0 {
		script = `
set -eu
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
for d in $(kubectl -n agyn-platform get deploy -o name); do
  kubectl -n agyn-platform rollout status "$d" --timeout=300s
done
`
	}
	_, err := Shell("sudo", "sh", "-c", script)
	return err
}
