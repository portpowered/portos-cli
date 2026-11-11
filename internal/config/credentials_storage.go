package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
	yamlv3 "gopkg.in/yaml.v3"
)

// CredentialsStorage handles credentials file operations
type CredentialsStorage struct {
	credentialsPath string
	mu              sync.RWMutex
	cached          *Credentials
}

// NewCredentialsStorage creates a new credentials storage handler
func NewCredentialsStorage(credentialsPath string) *CredentialsStorage {
	return &CredentialsStorage{
		credentialsPath: credentialsPath,
	}
}

// We parse the struct for tags with the koanf tag to determine the values to load.
const structTag = "koanf"

// Load loads credentials with hierarchy: defaults -> credentials file -> environment variables
// Results are cached in memory to avoid repeated file I/O
func (s *CredentialsStorage) Load() (*Credentials, error) {
	// Check cache first (read lock)
	s.mu.RLock()
	if s.cached != nil {
		cached := s.cached
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	// Load credentials (write lock)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if s.cached != nil {
		return s.cached, nil
	}

	k := koanf.New(".")

	// 1. Load default values using struct provider (empty credentials)
	defaultCreds := &Credentials{}
	if err := k.Load(structs.Provider(defaultCreds, structTag), nil); err != nil {
		return nil, fmt.Errorf("failed to load default credentials: %w", err)
	}

	// 2. Load credentials from YAML file (if exists)
	if err := k.Load(file.Provider(s.credentialsPath), yaml.Parser()); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load credentials file %s: %w", s.credentialsPath, err)
		}
		// File doesn't exist, continue with defaults
	}

	// 3. Load environment variables with PORTOS prefix (override file and defaults)
	if err := k.Load(env.Provider(EnvPrefixPORTOS, ".", func(s string) string {
		// Convert ACCESS_TOKEN (prefix already removed) to access_token
		// Keep underscores to match the koanf tag structure
		// i.e. to set the access_token environment variable, set PORTOS_ACCESS_TOKEN=...
		s = strings.ToLower(strings.TrimPrefix(s, EnvPrefixPORTOS))
		return strings.ToLower(s)
	}), nil); err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %w", err)
	}

	var creds Credentials
	if err := k.Unmarshal("", &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	// If PORTOS_ACCESS_TOKEN is set to an empty string, treat it as unset and keep
	// the file-stored token. An empty env var should not override a valid stored token.
	if creds.AccessToken == "" {
		if envVal := os.Getenv(EnvAccessToken); envVal == "" {
			// Env var either not set or explicitly empty — load token from file directly
			var fileCreds Credentials
			fileK := koanf.New(".")
			if err := fileK.Load(file.Provider(s.credentialsPath), yaml.Parser()); err == nil {
				if err := fileK.Unmarshal("", &fileCreds); err == nil {
					creds.AccessToken = fileCreds.AccessToken
				}
			}
		}
	}

	// Cache the loaded credentials
	s.cached = &creds

	return &creds, nil
}

// Save saves credentials to YAML file and invalidates the cache
func (s *CredentialsStorage) Save(creds *Credentials) error {
	// Resolve the credentials path to absolute if it's relative
	var credentialsPath string
	if filepath.IsAbs(s.credentialsPath) {
		credentialsPath = s.credentialsPath
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %w", err)
		}
		credentialsPath = filepath.Join(cwd, s.credentialsPath)
	}

	// Ensure directory exists
	dir := filepath.Dir(credentialsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create credentials directory %s: %w", dir, err)
	}

	data, err := yamlv3.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := writeFileAtomic(credentialsPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file %s: %w", credentialsPath, err)
	}

	// Invalidate cache and update with new credentials
	s.mu.Lock()
	s.cached = creds
	s.mu.Unlock()

	return nil
}

// writeFileAtomic writes data to a temp file in the target's directory and renames it over the target,
// so a concurrent reader (another port process) sees the old or the new file, never a torn one.
// os.Rename replaces an existing file on Windows as well.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Exists checks if the credentials file exists
func (s *CredentialsStorage) Exists() bool {
	_, err := os.Stat(s.credentialsPath)
	return !os.IsNotExist(err)
}

// Delete removes the credentials file and invalidates the cache
func (s *CredentialsStorage) Delete() error {
	if err := os.Remove(s.credentialsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete credentials file %s: %w", s.credentialsPath, err)
	}

	// Invalidate cache
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()

	return nil
}

// ClearCache clears the cached credentials (useful for testing)
func (s *CredentialsStorage) ClearCache() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// getDefaultConfig returns the default configuration with no API URL set.
// The caller (LoadConfig) validates that APIBaseURL is populated from env or config file.
func (s *ConfigStorage) getDefaultConfig() *Config {
	return &Config{}
}
