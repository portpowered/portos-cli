package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_HostedDefaultsSeparateAPIAuthAndBrowserOrigins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("api_base_url: https://api.portoperatingsystem.com/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := manager.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthBaseURL != "https://auth.portoperatingsystem.com" || cfg.BrowserAuthorizationBaseURL != "https://portoperatingsystem.com" {
		t.Fatalf("hosted origins mismatch: auth=%q browser=%q", cfg.AuthBaseURL, cfg.BrowserAuthorizationBaseURL)
	}
}

func TestLoadConfig_NoConfigTargetsHostedPortOS(t *testing.T) {
	t.Setenv(EnvAPIBaseURL, "")
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := manager.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() with no config file: %v", err)
	}
	if cfg.APIBaseURL != DefaultAPIBaseURL || cfg.AuthBaseURL != DefaultAuthBaseURL || cfg.BrowserAuthorizationBaseURL != "https://portoperatingsystem.com" {
		t.Fatalf("origins = api %q, auth %q, browser %q; want the hosted defaults", cfg.APIBaseURL, cfg.AuthBaseURL, cfg.BrowserAuthorizationBaseURL)
	}
}

func TestLoadConfig_AuthBaseURLFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Write config with both api_base_url and auth_base_url
	content := "api_base_url: https://api.example.com\nauth_base_url: https://auth.example.com\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := mgr.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.AuthBaseURL != "https://auth.example.com" {
		t.Errorf("AuthBaseURL = %q, want %q", cfg.AuthBaseURL, "https://auth.example.com")
	}
	if cfg.APIBaseURL != "https://api.example.com" {
		t.Errorf("APIBaseURL = %q, want %q", cfg.APIBaseURL, "https://api.example.com")
	}
}

func TestLoadConfig_AuthBaseURLFallsBackToAPIBaseURL(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Write config with only api_base_url (no auth_base_url)
	content := "api_base_url: https://api.example.com\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := mgr.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.AuthBaseURL != "https://api.example.com" {
		t.Errorf("AuthBaseURL = %q, want fallback to APIBaseURL %q", cfg.AuthBaseURL, "https://api.example.com")
	}
}

func TestLoadConfig_AuthBaseURLEnvOverridesFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Write config with both values
	content := "api_base_url: https://api.example.com\nauth_base_url: https://auth-from-file.example.com\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Set env var to override
	t.Setenv("PORTOS_AUTH_BASE_URL", "https://auth-from-env.example.com")

	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := mgr.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.AuthBaseURL != "https://auth-from-env.example.com" {
		t.Errorf("AuthBaseURL = %q, want env override %q", cfg.AuthBaseURL, "https://auth-from-env.example.com")
	}
}

func TestValidateAuthBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://api.example.com", false},
		{"valid http", "http://localhost:8080", false},
		{"invalid scheme", "ftp://example.com", true},
		{"missing scheme", "api.example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuthBaseURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAuthBaseURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestBrowserAuthorizationOriginConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, api, auth, configured, environment, expected string
	}{
		{"explicit website", "https://api.example.test", "https://auth.example.test", "https://portoperatingsystem.com", "", "https://portoperatingsystem.com"},
		{"custom deployment fallback", "https://api.example.test", "https://auth.example.test", "", "", "https://auth.example.test"},
		{"environment overrides website", "https://api.example.test", "https://auth.example.test", "https://website.example.test", "https://portoperatingsystem.com", "https://portoperatingsystem.com"},
		{"hosted production default", "https://api.portoperatingsystem.com", "https://auth.portoperatingsystem.com", "", "", "https://portoperatingsystem.com"},
		{"hosted production trailing slash", "https://api.portoperatingsystem.com/", "https://auth.portoperatingsystem.com/", "", "", "https://portoperatingsystem.com"},
		{"hosted explicit browser wins", "https://api.portoperatingsystem.com", "https://auth.portoperatingsystem.com", "https://website.example.test", "", "https://website.example.test"},
		{"hosted environment browser wins", "https://api.portoperatingsystem.com", "https://auth.portoperatingsystem.com", "", "https://website.example.test", "https://website.example.test"},
		{"custom auth with hosted API", "https://api.portoperatingsystem.com", "https://auth.example.test", "", "", "https://auth.example.test"},
		{"custom API with hosted auth", "https://api.example.test", "https://auth.portoperatingsystem.com", "", "", "https://auth.portoperatingsystem.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{EnvAPIBaseURL, EnvAuthBaseURL} {
				t.Setenv(name, "")
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(EnvBrowserAuthorizationBaseURL, tc.environment)
			if tc.environment == "" {
				if err := os.Unsetenv(EnvBrowserAuthorizationBaseURL); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := NewManager(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.SaveConfig(&Config{APIBaseURL: tc.api, AuthBaseURL: tc.auth, BrowserAuthorizationBaseURL: tc.configured}); err != nil {
				t.Fatal(err)
			}
			manager, err = NewManager(manager.ConfigDir())
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := manager.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.BrowserAuthorizationBaseURL != tc.expected || cfg.AuthBaseURL != tc.auth || cfg.APIBaseURL != tc.api {
				t.Fatal("browser entry point must not change API or token endpoint configuration")
			}
		})
	}
}

func TestBrowserAuthorizationOriginRejectsAmbiguousAndRemoteHTTPURLs(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"https://portoperatingsystem.com", true}, {"https://portoperatingsystem.com/", true},
		{"http://localhost:5173", true}, {"http://127.0.0.1:5173", true},
		{"http://remote.example.test", false}, {"javascript:alert(1)", false},
		{"https://user:password@example.test", false}, {"https://example.test/login", false},
		{"https://example.test?portos_request=bad", false}, {"https://example.test#fragment", false},
		{"https://", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			if err := validateBrowserAuthorizationBaseURL(tc.url); (err == nil) != tc.valid {
				t.Fatalf("validation error %v; valid=%v", err, tc.valid)
			}
		})
	}
}
