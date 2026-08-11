package local

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// The scripts that act on a running VM ship with the CLI rather than in the
// image. Both carry a value the image cannot know -- a token generated per
// install, and whatever port the host had free -- and both have to be fixable
// without rebuilding an image and recreating every VM booted from one.
//
//go:embed scripts/set-bootstrap-token.sh
var bootstrapTokenScript string

//go:embed scripts/set-ingress-port.sh
var ingressPortScript string

const (
	// BootstrapTokenPrefix marks a token this CLI minted for a local VM, so a
	// credential stored under the same profile by something else — a remote
	// cluster's API token, say — is never mistaken for one.
	BootstrapTokenPrefix = "agyn_local_"

	// bootstrapTokenBytes is the entropy behind a generated token. 32 bytes is
	// what the rest of the platform uses for service tokens.
	bootstrapTokenBytes = 32

	// platformNamespace holds the platform workloads inside the VM.
	platformNamespace = "agyn-platform"

	// systemOrganization is the declaration the release ships for the
	// organization platform-provisioned resources live in. Its status carries
	// the id the platform assigned, which is the organization every org-scoped
	// command runs against on a fresh VM.
	systemOrganization = "organization.platform.agyn.io/system"
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
// generated. It is handed over on stdin rather than as an argument, so it is in
// neither machine's process list.
func SetBootstrapToken(token string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("bootstrap token must not be empty")
	}
	out, err := RunScriptWithSecret(bootstrapTokenScript, strings.NewReader(token))
	if err != nil {
		return "", fmt.Errorf("install the bootstrap token in the VM: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// SetIngressPort tells the VM which port this host forwards, so the URLs it
// hands a browser are reachable.
//
// The image bakes a default, but the port belongs to the host: the default may
// already be taken. Only browser-facing URLs are affected — everything inside
// the cluster, the OpenZiti advertised addresses included, uses an internal
// port that never changes.
func SetIngressPort(port int, onDetail func(string)) (string, error) {
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("ingress port %d is out of range", port)
	}
	out, err := RunScriptProgress(ingressPortScript, onDetail, strconv.Itoa(port))
	if err != nil {
		return "", fmt.Errorf("point the platform's URLs at port %d: %w", port, err)
	}
	return strings.TrimSpace(out), nil
}

// OrganizationID reads the organization the release provisioned. The
// declaration records the assigned id on its own status, so this is read from
// the object the platform reconciles rather than from anything a script left
// behind -- and through limactl rather than over the network, like the CA.
func OrganizationID() (string, error) {
	out, err := Shell("sudo", "kubectl", "-n", platformNamespace,
		"get", systemOrganization, "-o", "jsonpath={.status.organizationId}")
	if err != nil {
		return "", fmt.Errorf("read the provisioned organization from the VM: %w", err)
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("the VM has not provisioned its system organization yet; %s reports no organizationId",
			systemOrganization)
	}
	return id, nil
}
