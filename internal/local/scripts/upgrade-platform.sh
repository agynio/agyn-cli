#!/usr/bin/env bash
set -euo pipefail

# Upgrades the platform Helm releases in place, without touching the VM.
#
# The alternative — replacing the whole disk image — throws away everything the
# user has in the VM: databases, threads, agents, workloads. That is a fine way
# to get a clean machine (`agyn local delete` then `agyn local start`) but a
# poor way to pick up a new platform release, because it is not reversible and
# not what "upgrade" sounds like it does.
#
# Values are reused from the installed release rather than re-rendered. The bake
# configured this cluster (OpenZiti addresses, OIDC, the in-cluster MinIO and
# OpenFGA endpoints), and `agyn local start` has since rewritten the bootstrap
# token. Re-rendering would silently revert it.
#
# Reusing values does NOT preserve the browser-facing port, because
# set-ingress-port.sh wrote that with `kubectl set env` rather than through the
# release. Helm rewrites those Deployments from chart values, so the port is
# re-applied at the end of this script.
#
# --values overlays a file on top of the reused values, for settings the image
# cannot know: an external OIDC issuer instead of the bundled Keycloak, say. It
# is an overlay, not a replacement — anything it does not name keeps the value
# the bake or `agyn local start` gave it.
#
# What this does NOT do: upgrade k3s, Istio, cert-manager, OpenZiti, or Postgres.
# Those come from the image, and moving them is what a new image is for.
#
# OUTPUT CONTRACT. stdout carries nothing but AGYN| markers, which the CLI turns
# into the steps a user sees; every tool's own output goes to stderr, which the
# CLI writes to a log. Helm's admission warnings, kubectl's klog lines and the
# release listing answer a different question than the one the user asked, and
# a step that fails prints the tail of that log with its path.

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

NAMESPACE="${AGYN_PLATFORM_NAMESPACE:-agyn-platform}"
PLATFORM_CHART="${AGYN_PLATFORM_CHART:-oci://ghcr.io/agynio/charts/agyn-platform}"
APPS_CHART="${AGYN_APPS_CHART:-oci://ghcr.io/agynio/charts/agyn-apps}"
HELM_TIMEOUT="${AGYN_HELM_TIMEOUT:-15m}"

# Named rather than positional: a values path and a chart version are easy to
# transpose, and the failure would be a silent no-op or a wrong chart.
platform_version=""
apps_version=""
extra_values=""
while [ "$#" -gt 0 ]; do
	case "${1}" in
	--values)
		extra_values="${2:-}"
		shift 2
		;;
	--platform-version)
		platform_version="${2:-}"
		shift 2
		;;
	--apps-version)
		apps_version="${2:-}"
		shift 2
		;;
	*)
		echo "unknown argument: ${1}" >&2
		exit 64
		;;
	esac
done

if [ -n "${extra_values}" ] && [ ! -r "${extra_values}" ]; then
	echo "values file not readable: ${extra_values}" >&2
	exit 66
fi

# The four markers the CLI understands. Anything else on stdout is treated as
# tool output and logged.
step() { printf 'AGYN|step|%s|%s\n' "${1}" "${2:-}"; }
done_() { printf 'AGYN|done|%s\n' "${1:-}"; }
skip() { printf 'AGYN|skip|%s\n' "${1:-}"; }
note() { printf 'AGYN|note|%s\n' "${1}"; }

if ! kubectl get --raw=/readyz >/dev/null 2>&1; then
	step "Waiting for the cluster" ""
	until kubectl get --raw=/readyz >/dev/null 2>&1; do
		sleep 5
	done
	done_ ""
fi

# Helm rewrites every Deployment it owns back to what the chart says. Anything
# running from source under `devspace dev` is one of those, so an upgrade ends
# those sessions — worth saying out loud, because the symptom is a service that
# quietly stops reflecting the code someone is editing.
if kubectl -n "${NAMESPACE}" get deploy -o jsonpath='{range .items[*]}{.spec.template.spec.containers[0].image}{"\n"}{end}' 2>/dev/null |
	grep -q 'devcontainer'; then
	note "services running from source (devspace) will be reset to their chart images"
fi

installed_version() {
	helm list -n "${NAMESPACE}" --filter "^${1}\$" -o json 2>/dev/null |
		sed -n 's/.*"chart":"[^"]*-\([0-9][^"]*\)".*/\1/p' | head -1
}

# The version the chart repository would install, so an upgrade can say where it
# is going before it goes there — and can decline to go nowhere.
available_version() {
	chart="${1}"
	want="${2}"
	set -- show chart "${chart}"
	if [ -n "${want}" ]; then
		set -- "$@" --version "${want}"
	fi
	helm "$@" 2>/dev/null | sed -n 's/^version: *//p' | head -1
}

upgrade() {
	release="${1}"
	chart="${2}"
	want="${3}"

	if ! helm status "${release}" -n "${NAMESPACE}" >/dev/null 2>&1; then
		return 0
	fi

	before="$(installed_version "${release}")"
	target="$(available_version "${chart}" "${want}")"

	# An upgrade to the version already installed still rewrites every workload
	# the chart owns and reports a new revision, so "nothing to do" and
	# "everything was replaced" would read alike. Say the first one instead.
	if [ -z "${extra_values}" ] && [ -n "${before}" ] && [ "${before}" = "${target}" ]; then
		step "${release}"
		skip "already at ${before}"
		return 0
	fi

	upgraded=1

	step "${release}" "${before:-unknown} → ${target:-latest}"

	# --reuse-values would carry the old values forward but ignore defaults the
	# newer chart introduces, so a subchart added since the last release starts
	# on its own empty defaults: keycloak arrived this way and looked for its
	# database on localhost. Resetting first takes the new chart's defaults and
	# then reapplies what the release actually set.
	set -- upgrade "${release}" "${chart}" -n "${NAMESPACE}" \
		--reset-then-reuse-values --wait --timeout "${HELM_TIMEOUT}"
	# Overlaid on top of the reused values, so the caller's file changes only
	# what it names and everything the bake configured survives.
	if [ -n "${extra_values}" ]; then
		set -- "$@" -f "${extra_values}"
	fi
	if [ -n "${target}" ]; then
		set -- "$@" --version "${target}"
	fi

	helm "$@" >&2

	after="$(installed_version "${release}")"
	done_ "${before:-unknown} → ${after:-unknown}"
}

upgraded=0
upgrade agyn-platform "${PLATFORM_CHART}" "${platform_version}"
upgrade agyn-apps "${APPS_CHART}" "${apps_version}"

# Helm rewrites every Deployment it owns back to what the chart says, and the
# browser-facing URLs are not in the chart: set-ingress-port.sh wrote them with
# `kubectl set env` when the host chose a port. An upgrade reverts them to the
# chart's default port, which now includes the OIDC issuer — leaving the Gateway
# fetching discovery from a port the ingress no longer publishes, and every
# login broken.
#
# The port itself survives, because it lives on the istio-ingressgateway
# Service and no chart owns that. So it is read back from there and re-applied.
# Idempotent: on a VM still using the default port this changes nothing.
#
# Skipped when nothing was upgraded, because then nothing reverted it: an
# upgrade that did nothing should say so in one line and stop, not perform
# repair work on a cluster it did not touch.
if [ "${upgraded}" -eq 0 ]; then
	exit 0
fi

host_port="$(kubectl -n istio-gateway get svc istio-ingressgateway \
	-o jsonpath='{.spec.ports[?(@.name=="https-hostport")].port}' 2>/dev/null || true)"
if [ -n "${host_port}" ] && [ -x /opt/agyn/set-ingress-port.sh ]; then
	step "Restoring the browser-facing port" ""
	/opt/agyn/set-ingress-port.sh "${host_port}" >&2
	done_ "${host_port}"
elif [ -n "${host_port}" ]; then
	note "WARNING: /opt/agyn/set-ingress-port.sh is missing, so browser-facing URLs now point at the chart's default port instead of ${host_port}"
fi

helm list -n "${NAMESPACE}" >&2
