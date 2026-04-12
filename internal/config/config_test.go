package config

import "testing"

func TestResolveGatewayURLUsesFlag(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv("AGYN_GATEWAY_URL", "https://env.example")
	t.Setenv("GATEWAY_ADDRESS", "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("https://flag.example")
	if got != "https://flag.example" {
		t.Fatalf("expected flag URL, got %q", got)
	}
}

func TestResolveGatewayURLPrefersAgynEnv(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv("AGYN_GATEWAY_URL", "https://env.example")
	t.Setenv("GATEWAY_ADDRESS", "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("")
	if got != "https://env.example" {
		t.Fatalf("expected AGYN_GATEWAY_URL, got %q", got)
	}
}

func TestResolveGatewayURLUsesGatewayAddress(t *testing.T) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}
	t.Setenv("GATEWAY_ADDRESS", "https://gateway.ziti")

	got := cfg.ResolveGatewayURL("")
	if got != "https://gateway.ziti" {
		t.Fatalf("expected GATEWAY_ADDRESS, got %q", got)
	}
}
