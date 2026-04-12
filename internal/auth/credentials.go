package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agynio/agyn-cli/internal/config"
)

type TokenOptions struct {
	AllowMissing bool
}

func LoadToken(opts TokenOptions) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	path := filepath.Join(home, config.ConfigDir, config.CredentialsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if opts.AllowMissing {
				return "", nil
			}
			return "", fmt.Errorf("no credentials found; run 'agyn auth login' or place a token in %s", path)
		}
		return "", fmt.Errorf("read credentials: %w", err)
	}

	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("empty credentials file: %s", path)
	}

	return token, nil
}

func SaveToken(token string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}

	dir := filepath.Join(home, config.ConfigDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	path := filepath.Join(dir, config.CredentialsFile)
	return os.WriteFile(path, []byte(token+"\n"), 0600)
}
