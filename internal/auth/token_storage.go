package auth

import (
	"fmt"

	"github.com/portpowered/portos-cli/internal/config"
)

// tokenStorage implements TokenStorage using the config manager provider
type tokenStorage struct {
	configProvider *config.ManagerProvider
}

// NewTokenStorage creates a new token storage instance
func NewTokenStorage(configProvider *config.ManagerProvider) TokenStorage {
	return &tokenStorage{
		configProvider: configProvider,
	}
}

// SaveCredentials saves authentication credentials using the config manager
func (ts *tokenStorage) SaveCredentials(creds *Credentials) error {
	if creds == nil {
		return fmt.Errorf("credentials cannot be nil")
	}
	mgr, err := ts.configProvider.GetManager()
	if err != nil {
		return err
	}
	// Convert auth.Credentials to config.Credentials
	configCreds := &config.Credentials{
		AccessToken:    creds.AccessToken,
		RefreshToken:   creds.RefreshToken,
		TokenType:      creds.TokenType,
		ExpiresAt:      creds.ExpiresAt,
		DPoPPrivateKey: creds.DPoPPrivateKey,
	}
	return mgr.SaveCredentials(configCreds)
}

// LoadCredentials loads authentication credentials from the config manager
func (ts *tokenStorage) LoadCredentials() (*Credentials, error) {
	mgr, err := ts.configProvider.GetManager()
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}
	configCreds, err := mgr.LoadCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}
	// Convert config.Credentials to auth.Credentials
	creds := &Credentials{
		AccessToken:    configCreds.AccessToken,
		RefreshToken:   configCreds.RefreshToken,
		TokenType:      configCreds.TokenType,
		ExpiresAt:      configCreds.ExpiresAt,
		DPoPPrivateKey: configCreds.DPoPPrivateKey,
	}
	return creds, nil
}

// DeleteCredentials removes stored credentials
func (ts *tokenStorage) DeleteCredentials() error {
	mgr, err := ts.configProvider.GetManager()
	if err != nil {
		return err
	}
	emptyCreds := &config.Credentials{}
	return mgr.SaveCredentials(emptyCreds)
}
