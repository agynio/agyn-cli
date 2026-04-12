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
	GatewayAddressEnv = "GATEWAY_ADDRESS"
)

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
	if flagURL != "" {
		return flagURL
	}
	if envURL := normalizeGatewayEnvURL(os.Getenv("AGYN_GATEWAY_URL")); envURL != "" {
		return envURL
	}
	if envURL := normalizeGatewayEnvURL(os.Getenv(GatewayAddressEnv)); envURL != "" {
		return envURL
	}
	return c.Gateway.URL
}

func normalizeGatewayEnvURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "http://" + trimmed
}
