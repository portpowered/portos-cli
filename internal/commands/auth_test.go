package commands

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
)

// mockAuthService is a minimal auth.Service for testing command output.
type mockAuthService struct{}

func (m *mockAuthService) SetToken(_ context.Context, _ string) error { return nil }
func (m *mockAuthService) Login(_ context.Context, _ string, handler auth.LoginMessageHandler) error {
	if handler != nil {
		handler("Visit http://example.com and enter code: ABCD-1234")
	}
	return nil
}
func (m *mockAuthService) BrowserLogin(_ context.Context, handler auth.LoginMessageHandler) error {
	return nil
}
func (m *mockAuthService) GetAccessToken(_ context.Context) (string, error) { return "", nil }
func (m *mockAuthService) RefreshToken(_ context.Context) error             { return nil }
func (m *mockAuthService) IsAuthenticated(_ context.Context) (bool, error)  { return false, nil }
func (m *mockAuthService) GetPrincipalPathFromToken(_ context.Context) (string, string, string, error) {
	return "", "", "", nil
}

// testWriter captures stdout and stderr.
type testWriter struct {
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newTestWriter() *testWriter {
	return &testWriter{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
}

func (w *testWriter) Stdout() io.Writer { return w.stdout }
func (w *testWriter) Stderr() io.Writer { return w.stderr }

func TestSetToken_HumanOutput_ContainsNextSteps(t *testing.T) {
	w := newTestWriter()
	logger := logging.NewLoggerRef()
	formatter := output.NewHumanFormatter(w, logger)
	cmd := NewAuthCommand(&mockAuthService{}, &config.ManagerProvider{}, formatter, logger)

	err := cmd.SetToken(context.Background(), SetTokenParams{Token: "test-token"})
	if err != nil {
		t.Fatalf("SetToken returned error: %v", err)
	}

	got := w.stdout.String()
	if !strings.Contains(got, "port endpoint list") {
		t.Errorf("SetToken human output should contain next-step suggestion mentioning 'port endpoint list'.\nGot:\n%s", got)
	}
}

func TestSetToken_JSONOutput_DoesNotContainNextSteps(t *testing.T) {
	w := newTestWriter()
	logger := logging.NewLoggerRef()
	formatter := output.NewJSONFormatter(w, logger)
	cmd := NewAuthCommand(&mockAuthService{}, &config.ManagerProvider{}, formatter, logger)

	err := cmd.SetToken(context.Background(), SetTokenParams{Token: "test-token"})
	if err != nil {
		t.Fatalf("SetToken returned error: %v", err)
	}

	got := w.stdout.String()
	if strings.Contains(got, postAuthNextSteps) {
		t.Errorf("SetToken JSON output should NOT contain next-step prose.\nGot:\n%s", got)
	}
	if strings.Contains(got, "port endpoint list") {
		t.Errorf("SetToken JSON output should NOT contain 'port endpoint list'.\nGot:\n%s", got)
	}
}

func TestLogin_HumanOutput_ContainsNextSteps(t *testing.T) {
	w := newTestWriter()
	logger := logging.NewLoggerRef()
	formatter := output.NewHumanFormatter(w, logger)
	cmd := NewAuthCommand(&mockAuthService{}, &config.ManagerProvider{}, formatter, logger)

	err := cmd.Login(context.Background(), LoginParams{ClientID: "test-client"})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	got := w.stdout.String()
	if !strings.Contains(got, "port endpoint list") {
		t.Errorf("Login human output should contain next-step suggestion mentioning 'port endpoint list'.\nGot:\n%s", got)
	}
}

func TestLogin_JSONOutput_DoesNotContainNextSteps(t *testing.T) {
	w := newTestWriter()
	logger := logging.NewLoggerRef()
	formatter := output.NewJSONFormatter(w, logger)
	cmd := NewAuthCommand(&mockAuthService{}, &config.ManagerProvider{}, formatter, logger)

	err := cmd.Login(context.Background(), LoginParams{ClientID: "test-client"})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	got := w.stdout.String()
	if strings.Contains(got, postAuthNextSteps) {
		t.Errorf("Login JSON output should NOT contain next-step prose.\nGot:\n%s", got)
	}
	if strings.Contains(got, "port endpoint list") {
		t.Errorf("Login JSON output should NOT contain 'port endpoint list'.\nGot:\n%s", got)
	}
}

func TestLogin_DeviceRunsTheDeviceFlowAndShowsTheCodeInJSON(t *testing.T) {
	w := newTestWriter()
	logger := logging.NewLoggerRef()
	cmd := NewAuthCommand(&mockAuthService{}, &config.ManagerProvider{}, output.NewJSONFormatter(w, logger), logger)

	if err := cmd.Login(context.Background(), LoginParams{Device: true}); err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if got := w.stdout.String(); !strings.Contains(got, "ABCD-1234") {
		t.Errorf("device login should print the user code even in JSON mode.\nGot:\n%s", got)
	}
}

func TestLogin_BrowserDoesNotRunTheDeviceFlow(t *testing.T) {
	w := newTestWriter()
	logger := logging.NewLoggerRef()
	cmd := NewAuthCommand(&mockAuthService{}, &config.ManagerProvider{}, output.NewHumanFormatter(w, logger), logger)

	if err := cmd.Login(context.Background(), LoginParams{}); err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if got := w.stdout.String(); strings.Contains(got, "ABCD-1234") {
		t.Errorf("browser login should not run the device flow.\nGot:\n%s", got)
	}
}
