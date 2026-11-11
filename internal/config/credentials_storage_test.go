package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// credentialsYAML is the exact file content the storage expects to load (YAML with koanf tag keys).
const credentialsYAML = `access_token: file-loaded-token
refresh_token: file-loaded-refresh
expires_at: 1700000000000
token_type: Bearer
dpop_private_key: ""
`

func TestCredentialsStorage_WithTempFile(t *testing.T) {
	clearEnv()

	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials.yaml")
	storage := NewCredentialsStorage(credentialsPath)

	t.Run("Load returns empty credentials when file does not exist", func(t *testing.T) {
		storage.ClearCache()
		creds, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if creds.AccessToken != "" || creds.RefreshToken != "" {
			t.Errorf("expected empty credentials, got access_token=%q refresh_token=%q", creds.AccessToken, creds.RefreshToken)
		}
	})

	t.Run("Exists returns false when file does not exist", func(t *testing.T) {
		if storage.Exists() {
			t.Error("Exists() = true, want false")
		}
	})

	t.Run("Load reads from actual credentials file on disk", func(t *testing.T) { checkLoadReadsFileOnDisk(t, storage, credentialsPath) })

	t.Run("Save creates file and Load returns saved credentials", func(t *testing.T) { checkSaveThenLoad(t, storage, credentialsPath) })

	t.Run("Load is cached after first load", func(t *testing.T) {
		creds1, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		creds2, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() second call error = %v", err)
		}
		if creds1 != creds2 {
			t.Error("Load() should return same cached instance on second call")
		}
	})

	t.Run("Delete removes file and clears cache", func(t *testing.T) {
		if err := storage.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if storage.Exists() {
			t.Error("Exists() = true after Delete(), want false")
		}
		storage.ClearCache()
		creds, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() after Delete() error = %v", err)
		}
		if creds.AccessToken != "" {
			t.Errorf("Load() after Delete() should return empty credentials, got access_token=%q", creds.AccessToken)
		}
	})
}

func checkLoadReadsFileOnDisk(t *testing.T, storage *CredentialsStorage, credentialsPath string) {
	storage.ClearCache()
	if err := os.MkdirAll(filepath.Dir(credentialsPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(credentialsPath, []byte(credentialsYAML), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	defer os.Remove(credentialsPath)

	creds, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if creds.AccessToken != "file-loaded-token" {
		t.Errorf("AccessToken = %q, want file-loaded-token", creds.AccessToken)
	}
	if creds.RefreshToken != "file-loaded-refresh" {
		t.Errorf("RefreshToken = %q, want file-loaded-refresh", creds.RefreshToken)
	}
	if creds.ExpiresAt != 1700000000000 {
		t.Errorf("ExpiresAt = %d, want 1700000000000", creds.ExpiresAt)
	}
	if creds.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", creds.TokenType)
	}
}

func checkSaveThenLoad(t *testing.T, storage *CredentialsStorage, credentialsPath string) {
	storage.ClearCache()
	want := &Credentials{
		AccessToken:  "token-abc",
		RefreshToken: "refresh-xyz",
		ExpiresAt:    1234567890000,
		TokenType:    "Bearer",
	}
	if err := storage.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !storage.Exists() {
		t.Error("Exists() = false after Save(), want true")
	}
	creds, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if creds.AccessToken != want.AccessToken || creds.RefreshToken != want.RefreshToken {
		t.Errorf("Load() = access_token=%q refresh_token=%q, want %q / %q", creds.AccessToken, creds.RefreshToken, want.AccessToken, want.RefreshToken)
	}
	if creds.ExpiresAt != want.ExpiresAt || creds.TokenType != want.TokenType {
		t.Errorf("Load() = expires_at=%d token_type=%q, want %d / %q", creds.ExpiresAt, creds.TokenType, want.ExpiresAt, want.TokenType)
	}
}

func clearEnv() {
	os.Unsetenv(EnvAccessToken)
	os.Unsetenv(EnvRefreshToken)
	os.Unsetenv(EnvExpiresAt)
	os.Unsetenv(EnvTokenType)
}

func saveEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

func restoreEnv(key, value string, had bool) {
	if had {
		os.Setenv(key, value)
	} else {
		os.Unsetenv(key)
	}
}

func TestCredentialsStorage_WithEnvironmentVariables(t *testing.T) {
	clearEnv()
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials.yaml")
	storage := NewCredentialsStorage(credentialsPath)

	t.Run("Load reads credentials from PORTOS_ prefixed env vars", func(t *testing.T) { checkLoadFromEnv(t, storage) })

	t.Run("Environment variables override file", func(t *testing.T) {
		storage.ClearCache()
		// Write file first
		fileCreds := &Credentials{
			AccessToken:  "file-token",
			RefreshToken: "file-refresh",
			ExpiresAt:    111,
			TokenType:    "Basic",
		}
		if err := storage.Save(fileCreds); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		at, atOk := saveEnv(EnvAccessToken)
		rt, rtOk := saveEnv(EnvRefreshToken)
		defer func() {
			restoreEnv(EnvAccessToken, at, atOk)
			restoreEnv(EnvRefreshToken, rt, rtOk)
		}()

		os.Setenv(EnvAccessToken, "override-token")
		os.Setenv(EnvRefreshToken, "override-refresh")
		storage.ClearCache() // force reload so env is merged

		creds, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if creds.AccessToken != "override-token" {
			t.Errorf("AccessToken = %q (env should override file), want override-token", creds.AccessToken)
		}
		if creds.RefreshToken != "override-refresh" {
			t.Errorf("RefreshToken = %q (env should override file), want override-refresh", creds.RefreshToken)
		}
		// ExpiresAt and TokenType not set in env here, so file values can remain (or defaults)
		// depending on koanf merge. We only assert env-override for the vars we set.
	})
}

func checkLoadFromEnv(t *testing.T, storage *CredentialsStorage) {
	storage.ClearCache()
	// Save current env to restore later
	at, atOk := saveEnv(EnvAccessToken)
	rt, rtOk := saveEnv(EnvRefreshToken)
	ea, eaOk := saveEnv(EnvExpiresAt)
	tt, ttOk := saveEnv(EnvTokenType)
	defer func() {
		restoreEnv(EnvAccessToken, at, atOk)
		restoreEnv(EnvRefreshToken, rt, rtOk)
		restoreEnv(EnvExpiresAt, ea, eaOk)
		restoreEnv(EnvTokenType, tt, ttOk)
	}()

	os.Setenv(EnvAccessToken, "env-access-token")
	os.Setenv(EnvRefreshToken, "env-refresh-token")
	os.Setenv(EnvExpiresAt, "999888777000")
	os.Setenv(EnvTokenType, "Bearer")

	creds, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if creds.AccessToken != "env-access-token" {
		t.Errorf("AccessToken = %q, want env-access-token", creds.AccessToken)
	}
	if creds.RefreshToken != "env-refresh-token" {
		t.Errorf("RefreshToken = %q, want env-refresh-token", creds.RefreshToken)
	}
	if creds.ExpiresAt != 999888777000 {
		t.Errorf("ExpiresAt = %d, want 999888777000", creds.ExpiresAt)
	}
	if creds.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", creds.TokenType)
	}
}

func writeCredentialsFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(credentialsYAML), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestCredentialsStorage_EmptyEnvVarFallback covers the three PORTOS_ACCESS_TOKEN states
// (unset, empty string, valid value) with and without a credentials file on disk.
func TestCredentialsStorage_EmptyEnvVarFallback(t *testing.T) {
	clearEnv()

	t.Run("env unset with file token uses file token", func(t *testing.T) {
		clearEnv()
		dir := t.TempDir()
		credPath := filepath.Join(dir, "credentials.yaml")
		writeCredentialsFile(t, credPath)
		storage := NewCredentialsStorage(credPath)

		creds, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if creds.AccessToken != "file-loaded-token" {
			t.Errorf("AccessToken = %q, want file-loaded-token", creds.AccessToken)
		}
	})

	t.Run("env empty string with file token uses file token", func(t *testing.T) {
		clearEnv()
		dir := t.TempDir()
		credPath := filepath.Join(dir, "credentials.yaml")
		writeCredentialsFile(t, credPath)
		storage := NewCredentialsStorage(credPath)

		os.Setenv(EnvAccessToken, "")
		defer os.Unsetenv(EnvAccessToken)

		creds, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if creds.AccessToken != "file-loaded-token" {
			t.Errorf("AccessToken = %q, want file-loaded-token (empty env var should fall through)", creds.AccessToken)
		}
	})

	t.Run("env valid value with file token uses env token", func(t *testing.T) { checkEnvTokenOverridesFile(t) })

	t.Run("env unset with no file returns empty token", func(t *testing.T) {
		clearEnv()
		dir := t.TempDir()
		credPath := filepath.Join(dir, "credentials.yaml")
		storage := NewCredentialsStorage(credPath)

		creds, err := storage.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if creds.AccessToken != "" {
			t.Errorf("AccessToken = %q, want empty string", creds.AccessToken)
		}
	})

	t.Run("env empty string with no file returns empty token", func(t *testing.T) { checkEmptyEnvNoFile(t) })
}

func checkEnvTokenOverridesFile(t *testing.T) {
	clearEnv()
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.yaml")
	writeCredentialsFile(t, credPath)
	storage := NewCredentialsStorage(credPath)

	os.Setenv(EnvAccessToken, "env-override-token")
	defer os.Unsetenv(EnvAccessToken)

	creds, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if creds.AccessToken != "env-override-token" {
		t.Errorf("AccessToken = %q, want env-override-token", creds.AccessToken)
	}
}

func checkEmptyEnvNoFile(t *testing.T) {
	clearEnv()
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.yaml")
	storage := NewCredentialsStorage(credPath)

	os.Setenv(EnvAccessToken, "")
	defer os.Unsetenv(EnvAccessToken)

	creds, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if creds.AccessToken != "" {
		t.Errorf("AccessToken = %q, want empty string", creds.AccessToken)
	}
}

func TestCredentialsStorage_SaveIsAtomicAndPrivate(t *testing.T) {
	clearEnv()
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yaml")
	storage := NewCredentialsStorage(path)

	// Overwriting an existing file must replace it completely.
	if err := os.WriteFile(path, []byte("access_token: stale\nrefresh_token: stale\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want := &Credentials{AccessToken: "token-abc", RefreshToken: "refresh-xyz", ExpiresAt: 1234567890000, TokenType: "Bearer"}
	if err := storage.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	storage.ClearCache()
	got, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.ExpiresAt != want.ExpiresAt || got.TokenType != want.TokenType {
		t.Fatalf("file content after Save = %+v, want %+v", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries after Save, want only the credentials file (no temp leftovers)", len(entries))
	}
}
