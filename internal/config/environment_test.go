package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) *Manager {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLoadEnvironment(t *testing.T) {
	m := writeConfig(t, "environments:\n  pilot:\n    apiUrl: https://pilot.example.com/\n    operationsAudience: aud-1\n")

	env, err := m.LoadEnvironment("pilot")
	if err != nil {
		t.Fatalf("LoadEnvironment: %v", err)
	}
	if env.APIURL != "https://pilot.example.com" || env.OperationsAudience != "aud-1" {
		t.Fatalf("unexpected environment: %+v", env)
	}
	if _, err := m.LoadEnvironment(""); err == nil {
		t.Fatal("empty environment name must error")
	}
	if _, err := m.LoadEnvironment("prod"); err == nil {
		t.Fatal("unknown environment must error")
	}
}

func TestLoadEnvironmentAuthURL(t *testing.T) {
	m := writeConfig(t, "environments:\n  pilot:\n    apiUrl: https://api.example.com\n    authUrl: https://auth.example.com/\n  local:\n    apiUrl: http://localhost:8080/\n")
	env, err := m.LoadEnvironment("pilot")
	if err != nil || env.AuthURL != "https://auth.example.com" || env.APIURL != "https://api.example.com" {
		t.Fatalf("split origins: %+v %v", env, err)
	}
	env, err = m.LoadEnvironment("local")
	if err != nil || env.AuthURL != "http://localhost:8080" {
		t.Fatalf("authUrl must default to apiUrl: %+v %v", env, err)
	}
}
