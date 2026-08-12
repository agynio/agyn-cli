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

	// dexConfigMap holds the shipped provider's config. Named for the chart's
	// resource, not the release: reading "dex" found nothing, and nothing is
	// how this reports "not the shipped provider" -- so the credentials simply
	// never printed.
	dexConfigMap = "dex-config"

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
func SetBootstrapToken(token string, onDetail func(string)) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("bootstrap token must not be empty")
	}
	out, err := RunScriptWithSecret(bootstrapTokenScript, strings.NewReader(token), onDetail)
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

// Account is a sign-in the bundled provider ships with.
type Account struct {
	Label    string
	Username string
	Password string
}

// bundledAccounts are the static logins the dex subchart ships. The passwords
// are bcrypt hashes in the chart, so the plaintext cannot be read back from a
// running VM -- it is a property of the shipped config, and printing it is only
// right while that config is the one in place.
//
// Named by address, which is the only thing Dex authenticates a static user by.
// Printing the local part instead is what sends someone into an account stored
// under "admin", against which the cluster-admin declaration for
// admin@<domain> has nothing to resolve -- a sign-in that works and a role that
// never arrives.
var bundledAccounts = []Account{
	{Label: "Regular user (recommended)", Username: "user@" + BaseDomain, Password: "user"},
	{Label: "Cluster admin", Username: "admin@" + BaseDomain, Password: "admin"},
}

// BundledAccounts returns the sign-ins to print, or nothing.
//
// Nothing unless the VM runs the shipped Dex config: Keycloak's accounts are
// different, an install pointed at its own issuer has none of ours, and a VM
// whose dex.users were replaced would be handed credentials that do not work.
// Each of those is worse than printing no credentials at all, so the check is
// for the two accounts this CLI knows the passwords of.
func BundledAccounts() []Account {
	config, err := Shell("sudo", "kubectl", "-n", platformNamespace,
		"get", "configmap", dexConfigMap, "-o", `jsonpath={.data.config\.yaml}`)
	if err != nil {
		return nil
	}
	for _, account := range bundledAccounts {
		if !strings.Contains(config, account.Username) {
			return nil
		}
	}
	return bundledAccounts
}
