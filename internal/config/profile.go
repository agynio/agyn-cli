package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Profile is a named set of connection settings: which Gateway to talk to,
// which organization to act in, and which CA to trust. Keeping them together
// under a name is what lets one machine address a local VM and a remote
// cluster without rewriting configuration between commands.
type Profile struct {
	GatewayURL   string `yaml:"gatewayUrl,omitempty"`
	Organization string `yaml:"organization,omitempty"`
	CAFile       string `yaml:"caFile,omitempty"`
}

const (
	// ProfileEnv selects a profile for one invocation.
	ProfileEnv = "AGYN_PROFILE"
	// DefaultProfileName is used when nothing has ever selected one.
	DefaultProfileName = "default"
	// LocalProfileName is the profile `agyn local start` provisions.
	LocalProfileName = "local"
)

// ResolveProfileName picks the profile for this invocation: an explicit flag
// beats the environment, which beats the recorded choice.
func (c *Config) ResolveProfileName(flag string) string {
	if name := strings.TrimSpace(flag); name != "" {
		return name
	}
	if name := strings.TrimSpace(os.Getenv(ProfileEnv)); name != "" {
		return name
	}
	if name := strings.TrimSpace(c.CurrentProfile); name != "" {
		return name
	}
	return DefaultProfileName
}

// Profile returns the named profile. A name that has never been configured
// yields an empty profile rather than an error: the gateway URL still falls
// back to its default, so a fresh machine can talk to a local cluster before
// anything has been written.
func (c *Config) Profile(name string) Profile {
	if c.Profiles == nil {
		return Profile{}
	}
	return c.Profiles[name]
}

// ProfileNames lists configured profiles in a stable order.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SetProfile creates or updates a profile, leaving fields the caller did not
// supply untouched so `profile set --organization` does not clear a gateway
// URL set earlier.
func (c *Config) SetProfile(name string, update Profile) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	existing := c.Profiles[name]
	if update.GatewayURL != "" {
		existing.GatewayURL = update.GatewayURL
	}
	if update.Organization != "" {
		existing.Organization = update.Organization
	}
	if update.CAFile != "" {
		existing.CAFile = update.CAFile
	}
	c.Profiles[name] = existing
}

// ResolveGatewayURLFor resolves the endpoint for a profile. A flag wins, then
// the environment, then the profile, then the legacy global setting, then the
// default — the legacy step keeps configurations written before profiles
// working without migration.
func (c *Config) ResolveGatewayURLFor(profileName, flag string) string {
	if url := strings.TrimSpace(flag); url != "" {
		return url
	}
	if url := strings.TrimSpace(os.Getenv(GatewayURLEnv)); url != "" {
		return url
	}
	if url := strings.TrimSpace(os.Getenv(GatewayAddressEnv)); url != "" {
		return url
	}
	if url := strings.TrimSpace(c.Profile(profileName).GatewayURL); url != "" {
		return url
	}
	if url := strings.TrimSpace(c.Gateway.URL); url != "" {
		return url
	}
	return DefaultGatewayURL
}

// ResolveOrganization returns the organization a command should act in: an
// explicit flag, otherwise whatever the profile has selected.
func (c *Config) ResolveOrganization(profileName, flag string) string {
	if org := strings.TrimSpace(flag); org != "" {
		return org
	}
	return strings.TrimSpace(c.Profile(profileName).Organization)
}

// ResolveCAFile returns the profile's extra CA bundle with ~ expanded.
func (c *Config) ResolveCAFile(profileName string) (string, error) {
	return ExpandPath(c.Profile(profileName).CAFile)
}

// ExpandPath resolves a leading ~ so configuration files can be written with
// portable paths.
func ExpandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// SaveProfiles persists the profile section and current selection without
// disturbing other config keys, including ones this CLI version does not know
// about — the same reason SaveLocal merges rather than rewrites.
func SaveProfiles(currentProfile string, profiles map[string]Profile) error {
	return mutateConfigFile(func(raw map[string]any) error {
		if strings.TrimSpace(currentProfile) != "" {
			raw["currentProfile"] = currentProfile
		}
		encoded := map[string]any{}
		for name, profile := range profiles {
			entry := map[string]any{}
			if profile.GatewayURL != "" {
				entry["gatewayUrl"] = profile.GatewayURL
			}
			if profile.Organization != "" {
				entry["organization"] = profile.Organization
			}
			if profile.CAFile != "" {
				entry["caFile"] = profile.CAFile
			}
			encoded[name] = entry
		}
		if len(encoded) == 0 {
			delete(raw, "profiles")
		} else {
			raw["profiles"] = encoded
		}
		return nil
	})
}
