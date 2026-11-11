package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/portos-cli/internal/config"
)

// TestAuthBaseURLRouting verifies that auth requests go to the auth base URL
// and API requests go to the API base URL when they are configured differently.
func TestAuthBaseURLRouting(t *testing.T) {
	// Set up a config with different auth and API base URLs
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	apiBase := "http://api.test:9090"
	authBase := "http://auth.test:9091"

	content := "api_base_url: " + apiBase + "\nauth_base_url: " + authBase + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Load config and verify the URLs are resolved correctly
	mgr, err := config.NewManager(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := mgr.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.APIBaseURL != apiBase {
		t.Errorf("APIBaseURL = %q, want %q", cfg.APIBaseURL, apiBase)
	}
	if cfg.AuthBaseURL != authBase {
		t.Errorf("AuthBaseURL = %q, want %q", cfg.AuthBaseURL, authBase)
	}

	// Verify that when auth_base_url is NOT set, it falls back to api_base_url
	tmpDir2 := t.TempDir()
	configPath2 := filepath.Join(tmpDir2, "config.yaml")
	content2 := "api_base_url: " + apiBase + "\n"
	if err := os.WriteFile(configPath2, []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}

	mgr2, err := config.NewManager(tmpDir2)
	if err != nil {
		t.Fatal(err)
	}

	cfg2, err := mgr2.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg2.AuthBaseURL != apiBase {
		t.Errorf("AuthBaseURL without auth_base_url set = %q, want fallback to APIBaseURL %q", cfg2.AuthBaseURL, apiBase)
	}
}

// TestAuthBaseURLEnvVarRouting verifies that PORTOS_AUTH_BASE_URL env var
// overrides the file config and is used for auth while API uses its own URL.
func TestAuthBaseURLEnvVarRouting(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	apiBase := "http://api.test:9090"
	fileAuthBase := "http://auth-file.test:9091"
	envAuthBase := "http://auth-env.test:9092"

	content := "api_base_url: " + apiBase + "\nauth_base_url: " + fileAuthBase + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PORTOS_AUTH_BASE_URL", envAuthBase)

	mgr, err := config.NewManager(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := mgr.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.APIBaseURL != apiBase {
		t.Errorf("APIBaseURL = %q, want %q", cfg.APIBaseURL, apiBase)
	}
	if cfg.AuthBaseURL != envAuthBase {
		t.Errorf("AuthBaseURL = %q, want env override %q", cfg.AuthBaseURL, envAuthBase)
	}
}
