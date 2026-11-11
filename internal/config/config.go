package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ConfigDirResolver returns the config directory path (may be empty for default).
// Implemented by *flags.GlobalFlags so the manager can be created after flags are parsed.
type ConfigDirResolver interface {
	ConfigDir() string
}

// ManagerProvider lazily creates a single Manager using the directory from the resolver.
// Initialize only once; the resolver is read on first GetManager() call (after CLI flags are parsed).
type ManagerProvider struct {
	resolver ConfigDirResolver
	once     sync.Once
	manager  *Manager
	initErr  error
}

// NewManagerProvider creates a provider that will create the Manager on first GetManager().
func NewManagerProvider(resolver ConfigDirResolver) *ManagerProvider {
	return &ManagerProvider{resolver: resolver}
}

// GetManager returns the config manager, creating it once using the resolver's config dir.
func (p *ManagerProvider) GetManager() (*Manager, error) {
	p.once.Do(func() {
		configDir := p.resolver.ConfigDir()
		if configDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				p.initErr = fmt.Errorf("failed to get user home directory: %w", err)
				return
			}
			configDir, err = filepath.Abs(filepath.Join(homeDir, ConfigDirName))
			if err != nil {
				p.initErr = fmt.Errorf("failed to resolve config directory: %w", err)
				return
			}
		} else {
			var err error
			configDir, err = filepath.Abs(configDir)
			if err != nil {
				p.initErr = fmt.Errorf("failed to resolve config directory: %w", err)
				return
			}
		}
		p.manager, p.initErr = NewManager(configDir)
	})
	return p.manager, p.initErr
}

// Config holds all configuration values for the CLI
type Config struct {
	APIBaseURL                  string `koanf:"api_base_url" yaml:"api_base_url"`
	AuthBaseURL                 string `koanf:"auth_base_url" yaml:"auth_base_url,omitempty"`
	BrowserAuthorizationBaseURL string `koanf:"browser_authorization_base_url" yaml:"browser_authorization_base_url,omitempty"`

	// Environments names the deployments operator commands can target, keyed by name.
	Environments map[string]Environment `koanf:"environments" yaml:"environments,omitempty"`
}

// Environment is one named deployment an operator command can target.
type Environment struct {
	APIURL string `koanf:"apiUrl" yaml:"apiUrl"`
	// AuthURL is the OAuth issuer and auth origin (token endpoint host and assertion aud). It defaults to
	// APIURL; split-origin deployments such as the pilot must set it.
	AuthURL            string `koanf:"authUrl" yaml:"authUrl,omitempty"`
	OperationsAudience string `koanf:"operationsAudience" yaml:"operationsAudience,omitempty"`
}

// Credentials holds authentication credentials
type Credentials struct {
	AccessToken    string `koanf:"access_token" yaml:"access_token"`
	RefreshToken   string `koanf:"refresh_token" yaml:"refresh_token"`
	ExpiresAt      int64  `koanf:"expires_at" yaml:"expires_at"` // milliseconds since epoch
	TokenType      string `koanf:"token_type" yaml:"token_type,omitempty"`
	DPoPPrivateKey string `koanf:"dpop_private_key" yaml:"dpop_private_key,omitempty"` // Base64-encoded Ed25519 private key for DPoP
}

// Manager manages configuration and credentials storage
type Manager struct {
	configStorage      *ConfigStorage
	credentialsStorage *CredentialsStorage
	configDir          string
}

// NewManager creates a new configuration manager for the given config directory.
// configDir must be an absolute path.
func NewManager(configDir string) (*Manager, error) {
	configPath := filepath.Join(configDir, ConfigFileName)
	credentialsPath := filepath.Join(configDir, CredentialsFileName)

	return &Manager{
		configStorage:      NewConfigStorage(configPath),
		credentialsStorage: NewCredentialsStorage(credentialsPath),
		configDir:          configDir,
	}, nil
}

// EnsureConfigDir creates the configuration directory if it doesn't exist
func (m *Manager) EnsureConfigDir() error {
	if err := os.MkdirAll(m.configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", m.configDir, err)
	}
	return nil
}

// LoadConfig loads configuration from the config.yaml file and the environment.
// With no API base URL configured it targets hosted Port OS, the same URL `port init` writes,
// so a freshly installed binary can sign in and run without a config file.
func (m *Manager) LoadConfig() (*Config, error) {
	cfg, err := m.configStorage.Load()
	if err != nil {
		return nil, err
	}

	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = DefaultAPIBaseURL
	}

	if err := validateAPIBaseURL(cfg.APIBaseURL); err != nil {
		return nil, err
	}

	// Hosted OAuth uses its authorization origin; custom deployments may share the API origin.
	if cfg.AuthBaseURL == "" {
		cfg.AuthBaseURL = cfg.APIBaseURL
		if strings.TrimRight(cfg.APIBaseURL, "/") == DefaultAPIBaseURL {
			cfg.AuthBaseURL = DefaultAuthBaseURL
		}
	}

	if err := validateAuthBaseURL(cfg.AuthBaseURL); err != nil {
		return nil, err
	}
	if cfg.BrowserAuthorizationBaseURL == "" {
		cfg.BrowserAuthorizationBaseURL = cfg.AuthBaseURL
		// The hosted browser owns device consent; the auth origin owns token exchange.
		if strings.TrimRight(cfg.APIBaseURL, "/") == "https://api.portoperatingsystem.com" && strings.TrimRight(cfg.AuthBaseURL, "/") == "https://auth.portoperatingsystem.com" {
			cfg.BrowserAuthorizationBaseURL = "https://portoperatingsystem.com"
		}
	} else if err := validateBrowserAuthorizationBaseURL(cfg.BrowserAuthorizationBaseURL); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadEnvironment returns the named entry of the config's environments map. It does not
// require api_base_url, so operator commands can run from a config that only lists environments.
func (m *Manager) LoadEnvironment(name string) (Environment, error) {
	if name == "" {
		return Environment{}, fmt.Errorf("--environment is required; add a named entry under environments in your CLI config")
	}
	cfg, err := m.configStorage.Load()
	if err != nil {
		return Environment{}, err
	}
	env, ok := cfg.Environments[name]
	if !ok {
		return Environment{}, fmt.Errorf("unknown environment %q; add it under environments in your CLI config", name)
	}
	if err := validateAPIBaseURL(env.APIURL); err != nil {
		return Environment{}, fmt.Errorf("environment %q: %w", name, err)
	}
	env.APIURL = strings.TrimRight(env.APIURL, "/")
	if env.AuthURL == "" {
		env.AuthURL = env.APIURL
	} else {
		if err := validateAuthBaseURL(env.AuthURL); err != nil {
			return Environment{}, fmt.Errorf("environment %q: %w", name, err)
		}
		env.AuthURL = strings.TrimRight(env.AuthURL, "/")
	}
	return env, nil
}

// SaveConfig saves configuration to the config.yaml file
func (m *Manager) SaveConfig(cfg *Config) error {
	return m.configStorage.Save(cfg)
}

// LoadCredentials loads credentials from the credentials.json file
func (m *Manager) LoadCredentials() (*Credentials, error) {
	return m.credentialsStorage.Load()
}

// SaveCredentials saves credentials to the credentials.json file
func (m *Manager) SaveCredentials(creds *Credentials) error {
	return m.credentialsStorage.Save(creds)
}

// IsAuthenticated checks if the user has valid credentials
func (m *Manager) IsAuthenticated() (bool, error) {
	creds, err := m.LoadCredentials()
	if err != nil {
		return false, err
	}

	if creds.AccessToken == "" {
		return false, nil
	}

	// Check if token is expired (with 5 minute buffer)
	// If ExpiresAt is 0, consider it valid (for testing with env vars)
	if creds.ExpiresAt > 0 {
		expiresAt := time.UnixMilli(creds.ExpiresAt)
		if expiresAt.Before(time.Now().Add(5 * time.Minute)) {
			return false, nil
		}
	}

	return true, nil
}

// ClearCredentialsCache clears the cached credentials (useful for testing)
func (m *Manager) ClearCredentialsCache() {
	m.credentialsStorage.ClearCache()
}

// ConfigDir returns the absolute config directory path used by this manager
func (m *Manager) ConfigDir() string {
	return m.configDir
}

// validateAPIBaseURL checks that the API base URL has a valid http or https scheme.
func validateAPIBaseURL(rawURL string) error {
	return validateBaseURL(rawURL, "API")
}

// validateAuthBaseURL checks that the auth base URL has a valid http or https scheme.
func validateAuthBaseURL(rawURL string) error {
	return validateBaseURL(rawURL, "auth")
}

func validateBrowserAuthorizationBaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return fmt.Errorf("browser authorization base URL must be an HTTPS origin or an HTTP loopback origin without user information, query or fragment")
	}
	ip := net.ParseIP(parsed.Hostname())
	loopback := strings.EqualFold(parsed.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return fmt.Errorf("browser authorization base URL requires HTTPS; HTTP is permitted only for loopback development")
	}
	return nil
}

// validateBaseURL checks that a base URL has a valid http or https scheme.
func validateBaseURL(rawURL, label string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid %s base URL '%s': %w. Must start with http:// or https://", label, rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid %s base URL '%s': must start with http:// or https://", label, rawURL)
	}
	return nil
}
