package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// CurrentProfile is the profile commands run under when nothing overrides
	// it. Empty means the default profile.
	CurrentProfile string             `yaml:"currentProfile,omitempty"`
	Profiles       map[string]Profile `yaml:"profiles,omitempty"`
	// Local configures the VM itself — image version, host ports, resources.
	// It is not a profile: the `local` profile under Profiles configures how
	// the CLI talks to that VM, and the two are independent.
	Local LocalConfig `yaml:"local"`
}

// LocalConfig configures the local platform VM managed by `agyn local`.
type LocalConfig struct {
	Port    int    `yaml:"port,omitempty"`
	APIPort int    `yaml:"apiPort,omitempty"`
	Version string `yaml:"version,omitempty"`
	CPUs    int    `yaml:"cpus,omitempty"`
	Memory  string `yaml:"memory,omitempty"`
}

const (
	DefaultGatewayURL = "https://gateway.agyn.dev"
	ConfigDir         = ".agyn"
	ConfigFile        = "config.yaml"
	CredentialsFile   = "credentials"
	GatewayURLEnv     = "AGYN_GATEWAY_URL"
	GatewayAddressEnv = "GATEWAY_ADDRESS"
	// TokenEnv supplies a token without writing one to disk, for CI.
	TokenEnv = "AGYN_TOKEN"
	// OrganizationEnv scopes commands for a shell session without changing the
	// profile's recorded selection.
	OrganizationEnv = "AGYN_ORGANIZATION"

	DefaultLocalPort    = 2496
	DefaultLocalAPIPort = 6445
	DefaultLocalVersion = "latest"
	DefaultLocalCPUs    = 4
	DefaultLocalMemory  = "8GiB"
)

// ApplyLocalDefaults fills unset local VM settings with defaults.
func (c *Config) ApplyLocalDefaults() {
	if c.Local.Port == 0 {
		c.Local.Port = DefaultLocalPort
	}
	if c.Local.APIPort == 0 {
		c.Local.APIPort = DefaultLocalAPIPort
	}
	if c.Local.Version == "" {
		c.Local.Version = DefaultLocalVersion
	}
	if c.Local.CPUs == 0 {
		c.Local.CPUs = DefaultLocalCPUs
	}
	if c.Local.Memory == "" {
		c.Local.Memory = DefaultLocalMemory
	}
}

// SaveLocal persists the local section without disturbing other config keys
// (including ones this CLI version does not know about).
func SaveLocal(local LocalConfig) error {
	encoded, err := yaml.Marshal(local)
	if err != nil {
		return fmt.Errorf("encode local config: %w", err)
	}
	localMap := map[string]any{}
	if err := yaml.Unmarshal(encoded, &localMap); err != nil {
		return fmt.Errorf("re-parse local config: %w", err)
	}
	return mutateConfigFile(func(raw map[string]any) error {
		raw["local"] = localMap
		return nil
	})
}

// mutateConfigFile applies a change to the config file as a raw map, so keys
// this CLI version does not model survive the write.
func mutateConfigFile(mutate func(map[string]any) error) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}

	dir := filepath.Join(home, ConfigDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	path := filepath.Join(dir, ConfigFile)
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}

	if err := mutate(raw); err != nil {
		return err
	}

	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

type GatewayTarget struct {
	URL      string
	UsesZiti bool
}

func Load() (*Config, error) {
	cfg := &Config{}

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

	return cfg, nil
}

// ResolveGatewayTargetFor resolves the endpoint for a profile.
func (c *Config) ResolveGatewayTargetFor(profileName, flagURL string) GatewayTarget {
	if flagURL != "" {
		url := normalizeGatewayURL(flagURL)
		return GatewayTarget{URL: url, UsesZiti: isZitiURL(url)}
	}
	// Inside an agent pod GATEWAY_ADDRESS names the in-cluster Ziti route and
	// the sidecar identity authenticates; profiles describe a developer
	// machine's endpoints and have nothing to say there.
	if envAddress := os.Getenv(GatewayAddressEnv); envAddress != "" {
		return GatewayTarget{URL: normalizeGatewayURL(envAddress), UsesZiti: true}
	}
	url := normalizeGatewayURL(c.ResolveGatewayURLFor(profileName, ""))
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
