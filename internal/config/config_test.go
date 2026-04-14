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
