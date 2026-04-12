package config

import "testing"

func TestResolveGatewayURLUsesFlag(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(AgynGatewayURLEnv, "https://env.example")
	t.Setenv(GatewayAddressEnv, "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("https://flag.example")
	if got != "https://flag.example" {
		t.Fatalf("expected flag URL, got %q", got)
	}
}

func TestResolveGatewayURLPrefersAgynEnv(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(AgynGatewayURLEnv, "https://env.example")
	t.Setenv(GatewayAddressEnv, "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("")
	if got != "https://env.example" {
		t.Fatalf("expected AGYN_GATEWAY_URL, got %q", got)
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

func TestResolveGatewayURLNormalizesAgynEnv(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(AgynGatewayURLEnv, "gateway.ziti")
	t.Setenv(GatewayAddressEnv, "https://gateway.agyn.dev")

	got := cfg.ResolveGatewayURL("")
	if got != "http://gateway.ziti" {
		t.Fatalf("expected normalized AGYN_GATEWAY_URL, got %q", got)
	}
}

func TestResolveGatewayURLNormalizesGatewayAddress(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv(AgynGatewayURLEnv, "")
	t.Setenv(GatewayAddressEnv, "gateway.ziti")

	got := cfg.ResolveGatewayURL("")
	if got != "http://gateway.ziti" {
		t.Fatalf("expected normalized GATEWAY_ADDRESS, got %q", got)
	}
}
