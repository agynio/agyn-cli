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
# Reusing values does NOT preserve the browser-facing port: it was written with
# `kubectl set env` rather than through the release, so Helm rewrites those
# Deployments from chart values. This script reports that something moved and
# the CLI points them back afterwards.
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
BASE_DOMAIN="${AGYN_BASE_DOMAIN:-agyn.dev}"

# Named rather than positional: a values path and a chart version are easy to
# transpose, and the failure would be a silent no-op or a wrong chart.
platform_version=""
apps_version=""
extra_values=""
ingress_port=""
resume=0
while [ "$#" -gt 0 ]; do
	case "${1}" in
	--resume)
		resume=1
		shift
		;;
	--ingress-port)
		ingress_port="${2:-}"
		shift 2
		;;
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
fail() { printf 'AGYN|fail|%s\n' "${1}"; }

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

# The release's own status, which is where a pending or failed state shows.
# `helm list` hides pending releases entirely, so it cannot be asked this.
#
# Every reader here ends in `|| true`. Under `set -e` with `pipefail` a
# substitution whose pipeline fails takes the whole script down, and these
# pipelines fail routinely and harmlessly: helm status errors for a release that
# is not installed, and helm show needs a network. Empty is the answer in both
# cases, and each caller already handles empty.
release_status() {
	{
		helm status "${1}" -n "${NAMESPACE}" -o json 2>/dev/null |
			sed -n 's/.*"info":{[^}]*"status":"\([a-z-]*\)".*/\1/p' | head -1
	} || true
}

installed_version() {
	{
		helm list -n "${NAMESPACE}" --filter "^${1}\$" -o json 2>/dev/null |
			sed -n 's/.*"chart":"[^"]*-\([0-9][^"]*\)".*/\1/p' | head -1
	} || true
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
	{ helm "$@" 2>/dev/null | sed -n 's/^version: *//p' | head -1; } || true
}

# The release's values still name the port the image was baked with, because
# nothing has ever written the host's choice into them: `agyn local start`
# patched the rendered Deployments instead. So every upgrade faithfully restores
# the baked port, the workloads holding those URLs crash-loop against a port
# that publishes nothing, and something has to patch them back afterwards --
# a second rollout of most of the platform, and a window in between long enough
# for Kubernetes to back off to five-minute retries.
#
# Correcting the values means the upgrade renders the host's port to begin with:
# one rollout, no window, and the release finally knows what port it is on.
#
# Derived from the release's own values rather than from a list of keys kept
# here, because there are eighteen of them across the services and a nineteenth
# added upstream would be missed silently.
port_overlay() {
	release="${1}"
	[ -n "${ingress_port}" ] || return 0

	current="$(helm get values "${release}" -n "${NAMESPACE}" -o yaml 2>/dev/null || true)"
	case "${current}" in
	"" | null) return 0 ;;
	esac

	# The port the values currently name, taken from the URLs themselves. Every
	# rewrite below replaces that exact number and nothing else, so a service
	# port that happens to appear -- 50051, 5432 -- is never touched.
	stale="$(printf '%s\n' "${current}" |
		sed -nE "s#.*://[A-Za-z0-9.-]*${BASE_DOMAIN}:([0-9]+).*#\1#p" | head -1)"
	if [ -z "${stale}" ] || [ "${stale}" = "${ingress_port}" ]; then
		return 0
	fi

	# Two shapes carry it: the URLs, and keycloak's ingressHostPort.port, which
	# is a plain number the chart models on its own.
	file="/run/agyn-values-${release}.yaml"
	printf '%s\n' "${current}" | sed -E \
		-e "s#(://[A-Za-z0-9.-]*${BASE_DOMAIN}):${stale}#\1:${ingress_port}#g" \
		-e "s#(^[[:space:]]*port:[[:space:]]*)${stale}[[:space:]]*\$#\1${ingress_port}#" >"${file}"
	printf '%s' "${file}"
}

upgrade() {
	release="${1}"
	chart="${2}"
	want="${3}"

	if ! helm status "${release}" -n "${NAMESPACE}" >/dev/null 2>&1; then
		return 0
	fi

	# An upgrade that was interrupted -- a laptop closed, a terminal killed --
	# leaves the release mid-flight, and Helm refuses every later attempt with
	# "another operation is in progress". That is true and unactionable, and the
	# state is invisible to `helm list`, which hides pending releases.
	# --resume is the caller saying they know: it has just cleared the in-flight
	# revision, and refusing here would refuse the recovery itself. Checked
	# against the flag rather than re-reading the status, so the recovery does
	# not depend on a second read agreeing with the first.
	status="$(release_status "${release}")"
	case "${status}" in
	pending-*)
		if [ "${resume}" -eq 0 ]; then
			step "${release}"
			fail "an earlier upgrade was interrupted and left it ${status}; continue it with: agyn local upgrade --resume"
			exit 70
		fi
		;;
	esac

	before="$(installed_version "${release}")"
	target="$(available_version "${chart}" "${want}")"

	overlay="$(port_overlay "${release}")"

	# An upgrade to the version already installed still rewrites every workload
	# the chart owns and reports a new revision, so "nothing to do" and
	# "everything was replaced" would read alike. Say the first one instead.
	#
	# Values that name the wrong port are their own reason to upgrade, chart
	# version or not: until they are corrected the release is one re-render away
	# from reverting to a port this VM does not serve.
	if [ -z "${extra_values}" ] && [ -z "${overlay}" ] &&
		[ -n "${before}" ] && [ "${before}" = "${target}" ]; then
		step "${release}"
		skip "already at ${before}"
		return 0
	fi

	upgraded=1

	if [ -n "${overlay}" ] && [ "${before}" = "${target}" ]; then
		step "${release}" "${before}, correcting the port to ${ingress_port}"
	else
		step "${release}" "${before:-unknown} → ${target:-latest}"
	fi

	# --reuse-values would carry the old values forward but ignore defaults the
	# newer chart introduces, so a subchart added since the last release starts
	# on its own empty defaults: keycloak arrived this way and looked for its
	# database on localhost. Resetting first takes the new chart's defaults and
	# then reapplies what the release actually set.
	#
	# Deliberately not --wait. An upgrade rewrites the browser-facing URLs back
	# to the chart's default port, and on a VM serving any other port the
	# workloads holding those URLs cannot start until they are pointed back --
	# which the CLI does after this returns. Waiting here waits for pods that
	# are waiting for the repair that is waiting for this: on a non-default port
	# the upgrade deadlocked until Helm's timeout, every time.
	#
	# --timeout still bounds the chart's hooks. The rollout is waited on by the
	# CLI afterwards, where it can also say which workloads are behind.
	set -- upgrade "${release}" "${chart}" -n "${NAMESPACE}" \
		--reset-then-reuse-values --timeout "${HELM_TIMEOUT}"
	# The host's port first, then the caller's file, so an explicit --values
	# can still override what this derived.
	if [ -n "${overlay}" ]; then
		set -- "$@" -f "${overlay}"
	fi
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
	if [ -n "${overlay}" ] && [ "${before}" = "${after}" ]; then
		done_ "${after}, now on port ${ingress_port}"
	else
		done_ "${before:-unknown} → ${after:-unknown}"
	fi
}

# Clears the revision an interrupted upgrade left in flight, so the upgrade that
# follows is not refused.
#
# Forward rather than back. Helm's own remedy is a rollback to the last deployed
# revision, and that is wrong here: an interruption leaves some workloads on the
# new chart already, and rolling those back moves them by however many versions
# the upgrade spanned. The platform's migrations are forward-only and guarantee
# one version of backward compatibility (see the database-migrations spec), so a
# ten-version step backwards is outside what any service promises to survive.
# Completing the upgrade is the direction the data has already gone.
#
# The in-flight revision is dropped by deleting its release record; the last
# deployed revision becomes current again in Helm's account, no workload is
# touched, and the upgrade below reconciles everything to the target.
clear_interrupted() {
	release="${1}"
	status="$(release_status "${release}")"
	case "${status}" in
	pending-*) ;;
	*) return 0 ;;
	esac

	revision="$({
		helm history "${release}" -n "${NAMESPACE}" -o json 2>/dev/null |
			sed -n 's/.*"revision":\([0-9]*\)[^0-9].*/\1/p' | tail -1
	} || true)"
	if [ -z "${revision}" ]; then
		return 0
	fi
	step "Clearing the interrupted upgrade of ${release}" "revision ${revision}, ${status}"
	kubectl delete secret "sh.helm.release.v1.${release}.v${revision}" -n "${NAMESPACE}" >&2
	done_ ""
}

if [ "${resume}" -eq 1 ]; then
	clear_interrupted agyn-platform
	clear_interrupted agyn-apps
fi

upgraded=0
upgrade agyn-platform "${PLATFORM_CHART}" "${platform_version}"
upgrade agyn-apps "${APPS_CHART}" "${apps_version}"

# Whether anything moved. The browser-facing URLs are rewritten from chart
# values by any upgrade, so they have to be pointed back at the host's port
# afterwards -- but only then. The CLI owns that step, because it is the side
# that knows which port this host forwards.
if [ "${upgraded}" -eq 1 ]; then
	printf 'AGYN|changed|\n'
fi

helm list -n "${NAMESPACE}" >&2
