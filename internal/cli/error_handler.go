package cli

import (
	"errors"
	"fmt"
	"strings"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
)

// defaultNotFoundHint is used when the resource kind is unknown or unset.
const defaultNotFoundHint = "Hint: The requested resource was not found. Check that the resource exists and that you have the correct identifier."

// authFailedMessage is the standard message for 401/JWT authentication failures.
const authFailedMessage = "authentication failed: token invalid or expired. Authenticate with 'port auth login', 'port auth set-token <token>', or PORTOS_ACCESS_TOKEN"

// WrapError maps known raw Go error patterns to user-facing messages.
// It inspects the error type and message to replace technical internals
// with clear, actionable guidance. The apiBaseURL parameter is included
// in connection error messages to help users diagnose misconfiguration.
func WrapError(err error, apiBaseURL string) error {
	if err == nil {
		return nil
	}

	// SetupErrors (e.g. from auth service) already carry user-facing messages
	// with remediation hints — pass them through without rewriting.
	var setupErr *apiclient.SetupError
	if errors.As(err, &setupErr) {
		return err
	}

	// Check for API errors with known status codes
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return errors.New(authFailedMessage)
		case 403:
			return errors.New("access denied: you do not have permission to perform this action. If this is unexpected, try 'port auth login' or 'port auth set-token <token>' to re-authenticate")
		case 404:
			originalMsg := "requested resource not found"
			if apiErr.Message != nil {
				originalMsg = *apiErr.Message
			}
			return fmt.Errorf("%s\n\n%s", originalMsg, defaultNotFoundHint)
		case 422:
			if apiErr.Message != nil && strings.Contains(*apiErr.Message, "unsupported message namespace") {
				return formatUnsupportedCapabilityError(*apiErr.Message)
			}
		}
	}

	// Match auth sentinel errors from the auth service
	switch {
	case errors.Is(err, auth.ErrTokenExpired), errors.Is(err, auth.ErrTokenInvalid):
		return errors.New(authFailedMessage)
	case errors.Is(err, auth.ErrNoCredentials):
		return errors.New(authFailedMessage)
	case errors.Is(err, apiclient.ErrUnauthorized):
		return errors.New(authFailedMessage)
	case errors.Is(err, apiclient.ErrForbidden):
		return errors.New("access denied: you do not have permission to perform this action. If this is unexpected, try 'port auth login' or 'port auth set-token <token>' to re-authenticate")
	}

	switch {
	case errors.Is(err, apiclient.ErrInvalidURL):
		return fmt.Errorf("invalid API base URL: the configured URL %q appears to be missing http:// or https://.\n  - Check that PORTOS_API_BASE_URL is set correctly\n  - Run 'port init' to reconfigure", displayURL(apiBaseURL))
	case errors.Is(err, apiclient.ErrConnectionFailed):
		return fmt.Errorf("connection failed: unable to reach the Port OS server at %s.\n  - Check that PORTOS_API_BASE_URL is set correctly\n  - Verify the backend server is running\n  - Run 'port init' to reconfigure the API URL", displayURL(apiBaseURL))
	}

	return err
}

// displayURL returns the URL for display, or a placeholder if empty.
func displayURL(url string) string {
	if url == "" {
		return "(not configured)"
	}
	return url
}

// formatUnsupportedCapabilityError rewrites a backend "unsupported message namespace"
// error into a user-friendly CLI message.
// Input format:  "unsupported message namespace: <namespace>. Supported: <a>, <b>"
// Output format: "Command not supported: <namespace>. This endpoint supports: <a>, <b>"
func formatUnsupportedCapabilityError(msg string) error {
	// Extract namespace and supported list from the backend message
	const prefix = "unsupported message namespace: "
	after := strings.TrimPrefix(msg, prefix)
	if after == msg {
		// Prefix not found — return as-is
		return errors.New(msg)
	}

	parts := strings.SplitN(after, ". Supported: ", 2)
	if len(parts) != 2 {
		return errors.New(msg)
	}

	namespace := parts[0]
	supported := parts[1]
	return fmt.Errorf("command not supported: %s. This endpoint supports: %s", namespace, supported)
}
