package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agynio/agyn-cli/internal/config"
	"gopkg.in/yaml.v3"
)

// profileCredential is one profile's stored secret. It is a struct rather than
// a bare string so a future credential kind can be added without rewriting
// everyone's file.
type profileCredential struct {
	Token string `yaml:"token,omitempty"`
}

// credentialsFile maps profile name to credential.
type credentialsFile map[string]profileCredential

// LoadTokenFor returns the token stored for a profile.
//
// Files written before profiles existed hold a single bare token with no
// profile name. Those are still read, and treated as belonging to whichever
// profile is asking: on such a machine there was only ever one endpoint, so
// there is no ambiguity to resolve.
func LoadTokenFor(profileName string, opts TokenOptions) (string, error) {
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if opts.AllowMissing {
				return "", nil
			}
			return "", credentialsNotFoundError{path: path}
		}
		return "", fmt.Errorf("read credentials: %w", err)
	}

	if legacy, ok := legacyToken(data); ok {
		return legacy, nil
	}

	var file credentialsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("parse credentials %s: %w", path, err)
	}
	token := strings.TrimSpace(file[profileName].Token)
	if token == "" {
		if opts.AllowMissing {
			return "", nil
		}
		return "", noProfileTokenError{path: path, profile: profileName}
	}
	return token, nil
}

// SaveTokenFor stores a token for one profile, leaving other profiles' tokens
// in place.
func SaveTokenFor(profileName, token string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	file := credentialsFile{}
	if data, err := os.ReadFile(path); err == nil {
		// A legacy bare token carries no profile name. It is migrated onto the
		// profile being written, which is the only profile it could have meant.
		if legacy, ok := legacyToken(data); ok {
			file[profileName] = profileCredential{Token: legacy}
		} else if err := yaml.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("parse credentials %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read credentials: %w", err)
	}

	file[profileName] = profileCredential{Token: strings.TrimSpace(token)}
	return writeCredentials(path, file)
}

// RemoveTokenFor drops one profile's token, used when a profile is removed or
// a local VM is purged.
func RemoveTokenFor(profileName string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read credentials: %w", err)
	}
	if _, ok := legacyToken(data); ok {
		// Nothing names a profile, so there is no single profile's token to
		// remove; clearing the file would discard a credential the caller did
		// not ask about.
		return nil
	}
	var file credentialsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse credentials %s: %w", path, err)
	}
	if _, exists := file[profileName]; !exists {
		return nil
	}
	delete(file, profileName)
	return writeCredentials(path, file)
}

// HasTokenFor reports whether a profile has a stored token, for listings that
// should show credential state without printing the secret.
func HasTokenFor(profileName string) bool {
	token, err := LoadTokenFor(profileName, TokenOptions{AllowMissing: true})
	return err == nil && token != ""
}

func writeCredentials(path string, file credentialsFile) error {
	data, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, config.ConfigDir, config.CredentialsFile), nil
}

// legacyToken recognises the pre-profile format: a file holding nothing but a
// token. A YAML mapping is the new format; anything else that is non-empty and
// single-line is the old one. The empty mapping "{}" is spelled out because it
// is what removing the last profile's token leaves behind, and reading it as a
// bare token would hand every profile a credential of "{}".
func legacyToken(data []byte) (string, bool) {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "{}" || strings.Contains(text, ":") || strings.Contains(text, "\n") {
		return "", false
	}
	return text, true
}

type noProfileTokenError struct {
	path    string
	profile string
}

func (e noProfileTokenError) Error() string {
	return fmt.Sprintf("no token stored for profile %q; run 'agyn auth set-token --profile %s' or 'agyn local credentials' (%s)",
		e.profile, e.profile, e.path)
}

func (e noProfileTokenError) Is(target error) bool {
	return target == ErrCredentialsNotFound
}
