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

// ConfigStorage handles configuration file operations
type ConfigStorage struct {
	configPath string
	mu         sync.RWMutex
	cached     *Config
}

// NewConfigStorage creates a new configuration storage handler
func NewConfigStorage(configPath string) *ConfigStorage {
	return &ConfigStorage{
		configPath: configPath,
	}
}

// Load loads configuration with hierarchy: defaults -> config file -> environment variables
// Results are cached in memory to avoid repeated file I/O
func (s *ConfigStorage) Load() (*Config, error) {
	// Check cache first (read lock)
	s.mu.RLock()
	if s.cached != nil {
		cached := s.cached
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	// Load config (write lock)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if s.cached != nil {
		return s.cached, nil
	}

	k := koanf.New(".")

	// 1. Load default values using struct provider
	defaultCfg := s.getDefaultConfig()
	if err := k.Load(structs.Provider(defaultCfg, structTag), nil); err != nil {
		return nil, fmt.Errorf("failed to load default config: %w", err)
	}

	// 2. Load configuration from YAML file (if exists)
	if err := k.Load(file.Provider(s.configPath), yaml.Parser()); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load config file %s: %w", s.configPath, err)
		}
		// File doesn't exist, continue with defaults
	}

	// 3. Load environment variables with PORTOS prefix (override file and defaults)
	if err := k.Load(env.Provider(EnvPrefixPORTOS, ".", func(s string) string {
		// Convert API_BASE_URL (prefix already removed) to api_base_url
		// Keep underscores to match the koanf tag structure
		s = strings.ToLower(strings.TrimPrefix(s, EnvPrefixPORTOS))
		return strings.ToLower(s)
	}), nil); err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Cache the loaded config
	s.cached = &cfg

	return &cfg, nil
}

// Save saves configuration to YAML file and invalidates the cache
func (s *ConfigStorage) Save(cfg *Config) error {
	// Ensure directory exists
	dir := filepath.Dir(s.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}

	data, err := yamlv3.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(s.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", s.configPath, err)
	}

	// Invalidate cache and update with new config
	s.mu.Lock()
	s.cached = cfg
	s.mu.Unlock()

	return nil
}

// Exists checks if the configuration file exists
func (s *ConfigStorage) Exists() bool {
	_, err := os.Stat(s.configPath)
	return !os.IsNotExist(err)
}
