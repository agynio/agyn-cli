package local

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	// BootstrapTokenPrefix marks a token this CLI minted for a local VM, so a
	// credential stored under the same profile by something else — a remote
	// cluster's API token, say — is never mistaken for one.
	BootstrapTokenPrefix = "agyn_local_"

	// bootstrapTokenBytes is the entropy behind a generated token. 32 bytes is
	// what the rest of the platform uses for service tokens.
	bootstrapTokenBytes = 32

	// bootstrapTokenScript ships in the image. It installs a host-supplied
	// token into the Gateway's secret and restarts the deployment, and is
	// idempotent: handing it the token already in place changes nothing.
	bootstrapTokenScript = "/opt/agyn/set-bootstrap-token.sh"

	// platformNamespace holds the platform workloads inside the VM.
	platformNamespace = "platform"

	// provisionConfigMap records what the image's build-time provisioning
	// created. The organization it names is the one every org-scoped command
	// runs against on a fresh VM.
	provisionConfigMap = "agyn-local-provision"
)

// GenerateBootstrapToken mints the Gateway bootstrap token for one install.
//
// The host generates it rather than the image baking one in: a baked value
// ships on the CDN, is identical on every machine running that image version,
// and cannot be rotated. The encoding is URL-safe and unpadded so the value
// survives a shell argument, a YAML scalar and an Authorization header without
// quoting or escaping.
func GenerateBootstrapToken() (string, error) {
	buf := make([]byte, bootstrapTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate bootstrap token: %w", err)
	}
	return BootstrapTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// IsBootstrapToken reports whether a stored credential is one this CLI minted
// for a local VM.
func IsBootstrapToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), BootstrapTokenPrefix)
}

// SetBootstrapToken installs the token this host holds into the running VM and
// returns the script's log output. The value travels over the limactl channel,
// never the network, and is never read back: the host keeps the copy it
// generated.
func SetBootstrapToken(token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("bootstrap token must not be empty")
	}
	out, err := Shell("sudo", bootstrapTokenScript, token)
	if err != nil {
		return "", fmt.Errorf("install the bootstrap token in the VM: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// OrganizationID reads the organization the image provisioned. It is recorded
// in a ConfigMap when the platform is baked, so it is readable as soon as the
// API server answers — before the Gateway itself serves traffic — and it is
// read through limactl rather than over the network, like the CA.
func OrganizationID() (string, error) {
	out, err := Shell("sudo", "kubectl", "-n", platformNamespace,
		"get", "configmap", provisionConfigMap, "-o", "jsonpath={.data.organizationId}")
	if err != nil {
		return "", fmt.Errorf("read the provisioned organization from the VM: %w", err)
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("the VM records no provisioned organization in configmap %s/%s; the image predates credential provisioning",
			platformNamespace, provisionConfigMap)
	}
	return id, nil
}
