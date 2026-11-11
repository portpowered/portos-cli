package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
)

type recordingLogin struct {
	cc  []auth.ClientCredentialsParams
	gcp []auth.GCPParams
}

func (r *recordingLogin) ClientCredentials(_ context.Context, p auth.ClientCredentialsParams) error {
	r.cc = append(r.cc, p)
	return nil
}

func (r *recordingLogin) GCPImpersonate(_ context.Context, p auth.GCPParams) error {
	r.gcp = append(r.gcp, p)
	return nil
}

func loginFixture(t *testing.T, yaml string) (*OperatorAuthCommand, *recordingLogin) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := logging.NewLoggerRef()
	rec := &recordingLogin{}
	return NewOperatorAuthCommand(rec, config.NewManagerProvider(dirResolver(dir)), output.NewJSONFormatter(newTestWriter(), logger), logger), rec
}

// The pilot is split-origin: the token endpoint and the assertion aud use the auth origin, not the API origin.
func TestOperatorLoginUsesAuthOriginWhenEnvironmentSplitsOrigins(t *testing.T) {
	cmd, rec := loginFixture(t, "environments:\n  pilot:\n    apiUrl: https://api.example.com\n    authUrl: https://auth.example.com/\n    operationsAudience: aud\n")
	if err := cmd.Login(context.Background(), OperatorLoginParams{ClientCredentials: true, ClientID: "c", KeyFile: "k", Environment: "pilot"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Login(context.Background(), OperatorLoginParams{GCPImpersonate: "sa", Environment: "pilot"}); err != nil {
		t.Fatal(err)
	}
	if rec.cc[0].AuthURL != "https://auth.example.com" || rec.gcp[0].AuthURL != "https://auth.example.com" {
		t.Fatalf("auth origin not used: %+v %+v", rec.cc, rec.gcp)
	}
}

func TestOperatorLoginAuthOriginDefaultsToAPIOrigin(t *testing.T) {
	cmd, rec := loginFixture(t, "environments:\n  local:\n    apiUrl: http://localhost:8080\n")
	if err := cmd.Login(context.Background(), OperatorLoginParams{ClientCredentials: true, ClientID: "c", KeyFile: "k", Environment: "local"}); err != nil {
		t.Fatal(err)
	}
	if rec.cc[0].AuthURL != "http://localhost:8080" {
		t.Fatalf("AuthURL = %q", rec.cc[0].AuthURL)
	}
}
