package config

import "testing"

func TestResolveGatewayURLUsesFlag(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(GatewayURLEnv, "https://env.example")
	t.Setenv(GatewayAddressEnv, "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("https://flag.example")
	if got != "https://flag.example" {
		t.Fatalf("expected flag URL, got %q", got)
	}
}

func TestResolveGatewayURLPrefersGatewayAddress(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(GatewayURLEnv, "https://env.example")
	t.Setenv(GatewayAddressEnv, "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("")
	if got != "https://gateway.ziti" {
		t.Fatalf("expected GATEWAY_ADDRESS, got %q", got)
	}
}

func TestResolveGatewayURLUsesGatewayAddress(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(GatewayAddressEnv, "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("")
	if got != "https://gateway.ziti" {
		t.Fatalf("expected GATEWAY_ADDRESS, got %q", got)
	}
}

func TestResolveGatewayURLNormalizesGatewayEnv(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(GatewayURLEnv, "gateway.ziti")
	t.Setenv(GatewayAddressEnv, "")

	got := cfg.ResolveGatewayURL("")
	if got != "http://gateway.ziti" {
		t.Fatalf("expected normalized AGYN_GATEWAY_URL, got %q", got)
	}
}

func TestResolveGatewayURLNormalizesGatewayAddress(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(GatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "gateway.ziti")

	got := cfg.ResolveGatewayURL("")
	if got != "http://gateway.ziti" {
		t.Fatalf("expected normalized GATEWAY_ADDRESS, got %q", got)
	}
}

func TestResolveGatewayTargetForUsesTheProfile(t *testing.T) {
	t.Setenv(GatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "")
	cfg := &Config{
		Gateway:  GatewayConfig{URL: "https://legacy.example"},
		Profiles: map[string]Profile{"local": {GatewayURL: "gateway.agyn.dev:2496"}},
	}

	target := cfg.ResolveGatewayTargetFor("local", "")
	if target.URL != "http://gateway.agyn.dev:2496" {
		t.Fatalf("expected the profile URL normalized, got %q", target.URL)
	}
	if target.UsesZiti {
		t.Fatal("expected a plain endpoint not to be treated as Ziti")
	}
	if got := cfg.ResolveGatewayTargetFor("other", "").URL; got != "https://legacy.example" {
		t.Fatalf("expected the pre-profile setting for an unconfigured profile, got %q", got)
	}
}

func TestResolveGatewayTargetForKeepsGatewayAddressAboveProfiles(t *testing.T) {
	// Inside an agent pod GATEWAY_ADDRESS names the in-cluster Ziti route and
	// the sidecar authenticates; a developer machine's profile has no say.
	t.Setenv(GatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "gateway.ziti")
	cfg := &Config{Profiles: map[string]Profile{"local": {GatewayURL: "https://local.example"}}}

	target := cfg.ResolveGatewayTargetFor("local", "")
	if target.URL != "http://gateway.ziti" || !target.UsesZiti {
		t.Fatalf("unexpected target %#v", target)
	}
	if got := cfg.ResolveGatewayTargetFor("local", "https://flag.example").URL; got != "https://flag.example" {
		t.Fatalf("expected the flag to win, got %q", got)
	}
}
