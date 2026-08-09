package config

import "testing"

func TestResolveGatewayTargetForUsesFlag(t *testing.T) {
	cfg := &Config{}
	t.Setenv(GatewayURLEnv, "https://env.example")
	t.Setenv(GatewayAddressEnv, "https://gateway.agyn")

	got := cfg.ResolveGatewayTargetFor(DefaultProfileName, "https://flag.example").URL
	if got != "https://flag.example" {
		t.Fatalf("expected flag URL, got %q", got)
	}
}

func TestResolveGatewayTargetForPrefersGatewayAddress(t *testing.T) {
	// GATEWAY_ADDRESS is injected in agent pods for in-cluster Ziti routing and
	// outranks anything a developer machine configured.
	cfg := &Config{}
	t.Setenv(GatewayURLEnv, "https://env.example")
	t.Setenv(GatewayAddressEnv, "https://gateway.agyn")

	got := cfg.ResolveGatewayTargetFor(DefaultProfileName, "").URL
	if got != "https://gateway.agyn" {
		t.Fatalf("expected GATEWAY_ADDRESS, got %q", got)
	}
}

func TestResolveGatewayTargetForNormalizesGatewayEnv(t *testing.T) {
	cfg := &Config{}
	t.Setenv(GatewayURLEnv, "gateway.agyn")
	t.Setenv(GatewayAddressEnv, "")

	got := cfg.ResolveGatewayTargetFor(DefaultProfileName, "").URL
	if got != "http://gateway.agyn" {
		t.Fatalf("expected normalized AGYN_GATEWAY_URL, got %q", got)
	}
}

func TestResolveGatewayTargetForNormalizesGatewayAddress(t *testing.T) {
	cfg := &Config{}
	t.Setenv(GatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "gateway.agyn")

	got := cfg.ResolveGatewayTargetFor(DefaultProfileName, "").URL
	if got != "http://gateway.agyn" {
		t.Fatalf("expected normalized GATEWAY_ADDRESS, got %q", got)
	}
}

func TestResolveGatewayTargetForFallsBackToDefault(t *testing.T) {
	cfg := &Config{}
	t.Setenv(GatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "")

	got := cfg.ResolveGatewayTargetFor("never-configured", "").URL
	if got != DefaultGatewayURL {
		t.Fatalf("expected the default URL, got %q", got)
	}
}

func TestResolveGatewayTargetForUsesTheProfile(t *testing.T) {
	t.Setenv(GatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "")
	cfg := &Config{
		Profiles: map[string]Profile{"local": {GatewayURL: "gateway.agyn.dev:2496"}},
	}

	target := cfg.ResolveGatewayTargetFor("local", "")
	if target.URL != "http://gateway.agyn.dev:2496" {
		t.Fatalf("expected the profile URL normalized, got %q", target.URL)
	}
	if target.UsesZiti {
		t.Fatal("expected a plain endpoint not to be treated as Ziti")
	}
	if got := cfg.ResolveGatewayTargetFor("other", "").URL; got != DefaultGatewayURL {
		t.Fatalf("expected the default URL for an unconfigured profile, got %q", got)
	}
}

func TestResolveGatewayTargetForKeepsGatewayAddressAboveProfiles(t *testing.T) {
	// Inside an agent pod GATEWAY_ADDRESS names the in-cluster Ziti route and
	// the sidecar authenticates; a developer machine's profile has no say.
	t.Setenv(GatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "gateway.agyn")
	cfg := &Config{Profiles: map[string]Profile{"local": {GatewayURL: "https://local.example"}}}

	target := cfg.ResolveGatewayTargetFor("local", "")
	if target.URL != "http://gateway.agyn" || !target.UsesZiti {
		t.Fatalf("unexpected target %#v", target)
	}
	if got := cfg.ResolveGatewayTargetFor("local", "https://flag.example").URL; got != "https://flag.example" {
		t.Fatalf("expected the flag to win, got %q", got)
	}
}
