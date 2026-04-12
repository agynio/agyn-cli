package cmd

import (
	"errors"
	"testing"

	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/config"
)

func TestLoadAuthTokenAllowsMissingCredentialsInPod(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.GatewayAddressEnv, "https://gateway.ziti")

	token, err := loadAuthToken("https://gateway.agyn.dev")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "" {
		t.Fatalf("expected empty token, got %q", token)
	}
}

func TestLoadAuthTokenAllowsMissingCredentialsForZitiURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.GatewayAddressEnv, "")

	token, err := loadAuthToken("https://gateway.ziti")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "" {
		t.Fatalf("expected empty token, got %q", token)
	}
}

func TestLoadAuthTokenMissingCredentialsOutsidePod(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.GatewayAddressEnv, "")

	_, err := loadAuthToken("https://gateway.agyn.dev")
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if !errors.Is(err, auth.ErrCredentialsNotFound) {
		t.Fatalf("expected credentials not found error, got %v", err)
	}
}
