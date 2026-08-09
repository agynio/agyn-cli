package config

import (
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// overlayHostnameSuffix is the TLD the platform's OpenZiti services intercept.
const overlayHostnameSuffix = ".agyn"

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

// LocalConfig configures the local platform VMs managed by `agyn local`.
//
// One VM is the ordinary case and stays unnamed in everyday use. The map exists
// for the cases a single VM cannot serve: moving data between versions an
// upgrade cannot bridge, or running separate clusters side by side.
type LocalConfig struct {
	// The flat fields are the shape from when only one VM existed. Load moves
	// them into Instances under the default name and clears them, so a config
	// written before this change keeps working and is never written back in
	// the old shape.
	Port    int    `yaml:"port,omitempty"`
	APIPort int    `yaml:"apiPort,omitempty"`
	Version string `yaml:"version,omitempty"`
	CPUs    int    `yaml:"cpus,omitempty"`
	Memory  string `yaml:"memory,omitempty"`

	// Current is the VM `agyn local` acts on when --instance is not given, set
	// by `agyn local select`. Empty means the default instance, so a machine
	// with one VM never sees this key.
	Current   string                   `yaml:"current,omitempty"`
	Instances map[string]LocalInstance `yaml:"instances,omitempty"`
}

// LocalInstance configures one VM.
type LocalInstance struct {
	Port    int    `yaml:"port,omitempty"`
	APIPort int    `yaml:"apiPort,omitempty"`
	Version string `yaml:"version,omitempty"`
	CPUs    int    `yaml:"cpus,omitempty"`
	Memory  string `yaml:"memory,omitempty"`
}

// migrateLocal folds a pre-multi-instance `local:` block into the instance map.
// Called on load, so nothing else in the CLI has to know the old shape existed.
func (c *Config) migrateLocal() {
	flat := LocalInstance{
		Port:    c.Local.Port,
		APIPort: c.Local.APIPort,
		Version: c.Local.Version,
		CPUs:    c.Local.CPUs,
		Memory:  c.Local.Memory,
	}
	c.Local.Port, c.Local.APIPort, c.Local.Version = 0, 0, ""
	c.Local.CPUs, c.Local.Memory = 0, ""

	if flat == (LocalInstance{}) {
		return
	}
	if c.Local.Instances == nil {
		c.Local.Instances = map[string]LocalInstance{}
	}
	// An explicit entry wins: if both shapes name the default instance, the
	// newer one is the one the user last wrote through the CLI.
	if _, ok := c.Local.Instances[DefaultInstanceName]; !ok {
		c.Local.Instances[DefaultInstanceName] = flat
	}
}

// InstanceSettings returns the stored settings for one VM with the
// version/resource defaults applied.
//
// Ports are left as stored: zero means "not chosen yet", and only the caller
// knows whether that should become the well-known default or a free port found
// for a second VM.
func (c *Config) InstanceSettings(name string) LocalInstance {
	settings := c.Local.Instances[name]
	if settings.Version == "" {
		settings.Version = DefaultLocalVersion
	}
	if settings.CPUs == 0 {
		settings.CPUs = DefaultLocalCPUs
	}
	if settings.Memory == "" {
		settings.Memory = DefaultLocalMemory
	}
	return settings
}

// ResolveInstanceName picks the VM a command acts on: an explicit --instance
// first, then the one `agyn local select` stored, then the default. A machine
// with one VM therefore never has to name it.
func (c *Config) ResolveInstanceName(flag string) string {
	if name := strings.TrimSpace(flag); name != "" {
		return name
	}
	if name := strings.TrimSpace(c.Local.Current); name != "" {
		return name
	}
	return DefaultInstanceName
}

// SaveCurrentInstance records the VM `agyn local` acts on by default.
func SaveCurrentInstance(name string) error {
	return mutateConfigFile(func(raw map[string]any) error {
		local, _ := raw["local"].(map[string]any)
		if local == nil {
			local = map[string]any{}
		}
		if name == "" || name == DefaultInstanceName {
			delete(local, "current")
		} else {
			local["current"] = name
		}
		raw["local"] = local
		return nil
	})
}

// InstanceNames lists the configured VMs, default first and the rest sorted, so
// output is stable between runs.
func (c *Config) InstanceNames() []string {
	names := make([]string, 0, len(c.Local.Instances))
	for name := range c.Local.Instances {
		if name != DefaultInstanceName {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if _, ok := c.Local.Instances[DefaultInstanceName]; ok {
		names = append([]string{DefaultInstanceName}, names...)
	}
	return names
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

	// DefaultInstanceName is the VM used when none is named — the ordinary
	// case, where the user has exactly one and never thinks about it.
	DefaultInstanceName = "agyn"

	DefaultLocalPort    = 2496
	DefaultLocalAPIPort = 6445
	DefaultLocalVersion = "latest"
	DefaultLocalCPUs    = 4
	DefaultLocalMemory  = "8GiB"
)

// SaveInstance persists one VM's settings without disturbing other config keys
// (including ones this CLI version does not know about), and without rewriting
// the other instances.
func SaveInstance(name string, settings LocalInstance) error {
	encoded, err := yaml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode local config: %w", err)
	}
	settingsMap := map[string]any{}
	if err := yaml.Unmarshal(encoded, &settingsMap); err != nil {
		return fmt.Errorf("re-parse local config: %w", err)
	}

	return mutateConfigFile(func(raw map[string]any) error {
		local, _ := raw["local"].(map[string]any)
		if local == nil {
			local = map[string]any{}
		}
		instances, _ := local["instances"].(map[string]any)
		if instances == nil {
			instances = map[string]any{}
		}
		instances[name] = settingsMap

		// Fold a pre-multi-instance block into the map on the way past, so the
		// file ends up in one shape rather than carrying both.
		if name == DefaultInstanceName {
			for _, key := range []string{"port", "apiPort", "version", "cpus", "memory"} {
				delete(local, key)
			}
		}
		local["instances"] = instances
		raw["local"] = local
		return nil
	})
}

// RemoveInstance drops a VM's settings from the config.
func RemoveInstance(name string) error {
	return mutateConfigFile(func(raw map[string]any) error {
		local, _ := raw["local"].(map[string]any)
		if local == nil {
			return nil
		}
		if instances, ok := local["instances"].(map[string]any); ok {
			delete(instances, name)
			local["instances"] = instances
		}
		if name == DefaultInstanceName {
			for _, key := range []string{"port", "apiPort", "version", "cpus", "memory"} {
				delete(local, key)
			}
		}
		raw["local"] = local
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
	cfg.migrateLocal()

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

// The overlay TLD is a suffix of the hostname, not a substring of the URL:
// the platform's own domains start with the same label, so gateway.agyn.dev
// would otherwise read as an overlay address and skip authentication.
func isZitiURL(rawURL string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(parsed.Hostname()), overlayHostnameSuffix)
}
