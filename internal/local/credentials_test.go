package local

import (
	"regexp"
	"testing"
)

// A generated token has to survive being passed as a shell argument to the
// in-VM installer, stored in a YAML credentials file, and sent as a bearer
// token, so nothing outside the URL-safe alphabet may appear in it.
var bootstrapTokenPattern = regexp.MustCompile(`^agyn_local_[A-Za-z0-9_-]{43}$`)

func TestGenerateBootstrapTokenIsURLSafeAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		token, err := GenerateBootstrapToken()
		if err != nil {
			t.Fatalf("generate bootstrap token: %v", err)
		}
		if !bootstrapTokenPattern.MatchString(token) {
			t.Fatalf("token %q is not a prefixed URL-safe value", token)
		}
		// Two machines running the same image version must not end up sharing a
		// credential, which is the whole reason the host generates it.
		if seen[token] {
			t.Fatalf("token %q was generated twice", token)
		}
		seen[token] = true
	}
}

func TestIsBootstrapTokenRecognisesOnlyItsOwn(t *testing.T) {
	token, err := GenerateBootstrapToken()
	if err != nil {
		t.Fatalf("generate bootstrap token: %v", err)
	}
	if !IsBootstrapToken(token) {
		t.Fatalf("expected %q to be recognised", token)
	}
	if !IsBootstrapToken("  " + token + "\n") {
		t.Fatal("expected surrounding whitespace to be tolerated")
	}
	for _, foreign := range []string{"", "agyn_api_token_value", "some-remote-cluster-token"} {
		if IsBootstrapToken(foreign) {
			t.Fatalf("expected %q not to be taken for a local bootstrap token", foreign)
		}
	}
}

func TestSetBootstrapTokenRejectsAnEmptyValue(t *testing.T) {
	// The script refuses an empty read too, but this check is what keeps the
	// VM from being reached at all for a value that cannot be right.
	if _, err := SetBootstrapToken("   ", nil); err == nil {
		t.Fatal("expected an empty token to be rejected before touching the VM")
	}
}

func TestGatewayURLCarriesTheIngressPort(t *testing.T) {
	if got := GatewayURL(2496); got != "https://gateway.agyn.dev:2496" {
		t.Fatalf("gateway URL = %q", got)
	}
}
