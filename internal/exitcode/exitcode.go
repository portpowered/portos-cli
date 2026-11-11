// Package exitcode maps CLI errors to the process exit codes scripts branch on.
package exitcode

import (
	"errors"
	"net/http"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/spf13/cobra"
)

// Exit codes returned by the port CLI.
const (
	ExitOK               = 0
	ExitFailure          = 1
	ExitInvalid          = 2
	ExitAuth             = 3
	ExitNotCarriedOut    = 10
	ExitSessionNotActive = 11
	ExitBusy             = 12
	ExitHold             = 13
	ExitCamera           = 14
)

var providerCodeExits = map[string]int{
	"remote_control_move_not_carried_out": ExitNotCarriedOut,
	"remote_control_not_active":           ExitSessionNotActive,
	"remote_control_not_started":          ExitSessionNotActive,
	"remote_control_move_in_progress":     ExitBusy,
	"go_to_point_failed":                  ExitNotCarriedOut,
	"go_to_point_timeout":                 ExitNotCarriedOut,
	"go_to_point_state_unavailable":       ExitFailure,
	"go_to_point_session_lost":            ExitSessionNotActive,
	"talkback_busy":                       ExitBusy,
	"hold_lease_required":                 ExitHold,
	"hold_owned_by_other":                 ExitHold,
	"hold_owns_session":                   ExitHold,
	"hold_ended":                          ExitHold,
	"hold_requires_refreshable_login":     ExitHold,
	"camera_refused":                      ExitCamera,
	"camera_unavailable":                  ExitCamera,
	"camera_pin_required":                 ExitCamera,
}

// messageOnlyExits are provider codes that exit by code only when a message
// reply carries them. The host's hold routes also use hold_not_found (a 404
// on release), which keeps its own exit code.
var messageOnlyExits = map[string]int{
	"hold_not_found":  ExitHold,
	"invalid_request": ExitInvalid,
}

// InvalidArgumentError marks a flag or argument mistake; it exits with ExitInvalid.
type InvalidArgumentError struct {
	Err error
}

// NewInvalidArgument wraps err as an invalid flag or argument error.
func NewInvalidArgument(err error) error {
	return &InvalidArgumentError{Err: err}
}

func (e *InvalidArgumentError) Error() string { return e.Err.Error() }
func (e *InvalidArgumentError) Unwrap() error { return e.Err }

// Code returns the process exit code for err.
func Code(err error) int {
	if err == nil {
		return ExitOK
	}

	var invalid *InvalidArgumentError
	if errors.As(err, &invalid) {
		return ExitInvalid
	}

	var failed *apiclient.MessageRequestFailed
	if errors.As(err, &failed) {
		provider := ProviderCode(failed.Request)
		if code, ok := providerCodeExits[provider]; ok {
			return code
		}
		if code, ok := messageOnlyExits[provider]; ok {
			return code
		}
		return ExitFailure
	}

	// A refusal from the session host's hold routes carries the host code.
	var held interface{ HoldCode() string }
	if errors.As(err, &held) {
		if code, ok := providerCodeExits[held.HoldCode()]; ok {
			return code
		}
	}

	if errors.Is(err, auth.ErrNoCredentials) || errors.Is(err, auth.ErrTokenExpired) || errors.Is(err, auth.ErrTokenInvalid) {
		return ExitAuth
	}

	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden:
			return ExitAuth
		case apiErr.StatusCode >= 400 && apiErr.StatusCode < 500:
			return ExitInvalid
		}
	}

	return ExitFailure
}

// ProviderCode returns the first error's details.providerCode, else its error code,
// or "" when the request recorded no errors.
func ProviderCode(request *api.MessageRequest) string {
	if request == nil || len(request.Errors) == 0 {
		return ""
	}
	first := request.Errors[0].Error
	if first == nil {
		return ""
	}
	if first.Details != nil && first.Details.ProviderCode != nil && *first.Details.ProviderCode != "" {
		return *first.Details.ProviderCode
	}
	return string(first.Code)
}

// TrackUsageErrors wraps every runnable command under root so that an error Cobra
// returns before any command body runs (unknown flag, wrong argument count, missing
// required flag, unknown subcommand) is classified as an invalid-argument error.
// Call the returned function on the error from root.Execute.
func TrackUsageErrors(root *cobra.Command) func(error) error {
	ran := false
	var wrap func(*cobra.Command)
	wrap = func(cmd *cobra.Command) {
		if run := cmd.RunE; run != nil {
			cmd.RunE = func(c *cobra.Command, args []string) error {
				ran = true
				return run(c, args)
			}
		} else if run := cmd.Run; run != nil {
			cmd.Run = func(c *cobra.Command, args []string) {
				ran = true
				run(c, args)
			}
		}
		for _, child := range cmd.Commands() {
			wrap(child)
		}
	}
	wrap(root)
	return func(err error) error {
		if err == nil || ran {
			return err
		}
		return NewInvalidArgument(err)
	}
}
