package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Gateway GatewayConfig `yaml:"gateway"`
}

type GatewayConfig struct {
	URL string `yaml:"url"`
}

const (
	DefaultGatewayURL = "https://gateway.agyn.dev"
	ConfigDir         = ".agyn"
	ConfigFile        = "config.yaml"
	CredentialsFile   = "credentials"
	GatewayURLEnv     = "AGYN_GATEWAY_URL"
	GatewayAddressEnv = "GATEWAY_ADDRESS"
)

type GatewayTarget struct {
	URL      string
	UsesZiti bool
}

func Load() (*Config, error) {
	cfg := &Config{Gateway: GatewayConfig{URL: DefaultGatewayURL}}

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, nil
	}

	path := filepath.Join(home, ConfigDir, ConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Gateway.URL == "" {
		cfg.Gateway.URL = DefaultGatewayURL
	}

	return cfg, nil
}

func (c *Config) ResolveGatewayURL(flagURL string) string {
	return c.ResolveGatewayTarget(flagURL).URL
}

func (c *Config) ResolveGatewayTarget(flagURL string) GatewayTarget {
	if flagURL != "" {
		url := normalizeGatewayURL(flagURL)
		return GatewayTarget{URL: url, UsesZiti: isZitiURL(url)}
	}
	if envAddress := os.Getenv(GatewayAddressEnv); envAddress != "" {
		return GatewayTarget{URL: normalizeGatewayURL(envAddress), UsesZiti: true}
	}
	if envURL := os.Getenv(GatewayURLEnv); envURL != "" {
		url := normalizeGatewayURL(envURL)
		return GatewayTarget{URL: url, UsesZiti: isZitiURL(url)}
	}
	url := normalizeGatewayURL(c.Gateway.URL)
	return GatewayTarget{URL: url, UsesZiti: isZitiURL(url)}
}

func normalizeGatewayURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return DefaultGatewayURL
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "http://" + trimmed
}

func isZitiURL(url string) bool {
	return strings.Contains(strings.ToLower(url), ".ziti")
}
