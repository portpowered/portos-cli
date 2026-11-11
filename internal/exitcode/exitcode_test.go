package exitcode

import (
	"errors"
	"fmt"
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/spf13/cobra"
)

func failedWith(providerCode string) *apiclient.MessageRequestFailed {
	return &apiclient.MessageRequestFailed{Request: &api.MessageRequest{
		Id:     "req-1",
		Status: api.FAILED,
		Errors: []api.MessageRequestError{{
			TargetId: "ep/1",
			Error:    &api.Error{Message: "failed", Details: &api.ErrorDetails{ProviderCode: &providerCode}},
		}},
	}}
}

func TestExitCodeClasses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"plain", errors.New("x"), ExitFailure},
		{"wrapped plain", fmt.Errorf("a: %w", errors.New("x")), ExitFailure},
		{"bad request", &apiclient.APIError{StatusCode: 400, Err: errors.New("bad")}, ExitInvalid},
		{"unprocessable", &apiclient.APIError{StatusCode: 422, Err: errors.New("bad")}, ExitInvalid},
		{"not found", &apiclient.APIError{StatusCode: 404, Err: errors.New("nf")}, ExitInvalid},
		{"unauthorized", &apiclient.APIError{StatusCode: 401, Err: errors.New("no")}, ExitAuth},
		{"forbidden", &apiclient.APIError{StatusCode: 403, Err: errors.New("no")}, ExitAuth},
		{"server error", &apiclient.APIError{StatusCode: 503, Err: errors.New("down")}, ExitFailure},
		{"no credentials", fmt.Errorf("send: %w", auth.ErrNoCredentials), ExitAuth},
		{"token expired", auth.ErrTokenExpired, ExitAuth},
		{"token invalid", auth.ErrTokenInvalid, ExitAuth},
		{"setup error", apiclient.NewSetupError("misconfigured"), ExitFailure},
		{"invalid argument", NewInvalidArgument(errors.New("bad flag")), ExitInvalid},
		{"wrapped invalid argument", fmt.Errorf("x: %w", NewInvalidArgument(errors.New("bad"))), ExitInvalid},
		{"host hold error", fmt.Errorf("open: %w", hostCoded("hold_owned_by_other")), ExitHold},
		{"host unrelated error", hostCoded("host_closing"), ExitFailure},
		{"not carried out", failedWith("remote_control_move_not_carried_out"), ExitNotCarriedOut},
		{"not active", failedWith("remote_control_not_active"), ExitSessionNotActive},
		{"not started", failedWith("remote_control_not_started"), ExitSessionNotActive},
		{"move in progress", failedWith("remote_control_move_in_progress"), ExitBusy},
		{"talkback busy", failedWith("talkback_busy"), ExitBusy},
		{"hold lease required", failedWith("hold_lease_required"), ExitHold},
		{"hold owned by other", failedWith("hold_owned_by_other"), ExitHold},
		{"hold owns session", failedWith("hold_owns_session"), ExitHold},
		{"hold ended", failedWith("hold_ended"), ExitHold},
		{"hold refreshable", failedWith("hold_requires_refreshable_login"), ExitHold},
		{"camera refused", failedWith("camera_refused"), ExitCamera},
		{"camera pin", failedWith("camera_pin_required"), ExitCamera},
		{"unknown provider code", failedWith("something_else"), ExitFailure},
		{"wrapped request failure", fmt.Errorf("x: %w", failedWith("talkback_busy")), ExitBusy},
		{"failure without errors", &apiclient.MessageRequestFailed{Request: &api.MessageRequest{Id: "r", Status: api.FAILED}}, ExitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Fatalf("Code() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestProviderCode(t *testing.T) {
	if got := ProviderCode(failedWith("camera_refused").Request); got != "camera_refused" {
		t.Fatalf("details provider code: %q", got)
	}
	noDetails := &api.MessageRequest{Errors: []api.MessageRequestError{{Error: &api.Error{Code: api.ErrorCode("CONFLICT")}}}}
	if got := ProviderCode(noDetails); got != "CONFLICT" {
		t.Fatalf("falls back to error code: %q", got)
	}
	if got := ProviderCode(&api.MessageRequest{}); got != "" {
		t.Fatalf("no errors: %q", got)
	}
	if got := ProviderCode(nil); got != "" {
		t.Fatalf("nil request: %q", got)
	}
}

func executeTracked(t *testing.T, args ...string) error {
	t.Helper()
	root := &cobra.Command{Use: "port", SilenceErrors: true, SilenceUsage: true}
	run := &cobra.Command{Use: "run <name>", Args: cobra.ExactArgs(1), RunE: func(*cobra.Command, []string) error {
		return errors.New("command failed")
	}}
	run.Flags().Bool("wait", false, "")
	root.AddCommand(run)
	classify := TrackUsageErrors(root)
	root.SetArgs(args)
	return classify(root.Execute())
}

func TestUsageErrorsExitInvalid(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown flag":       {"run", "x", "--nope"},
		"wrong arg count":    {"run"},
		"unknown subcommand": {"missing"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Code(executeTracked(t, args...)); got != ExitInvalid {
				t.Fatalf("Code() = %d, want %d", got, ExitInvalid)
			}
		})
	}
	if got := Code(executeTracked(t, "run", "x")); got != ExitFailure {
		t.Fatalf("an error from the command itself must stay a failure, got %d", got)
	}
}

type hostCoded string

func (h hostCoded) Error() string    { return string(h) }
func (h hostCoded) HoldCode() string { return string(h) }
