package api

import "errors"

// Sentinel errors for HTTP status code classification.
// The error handler uses errors.Is() against these to identify 401/403
// responses without relying on substring matching of status text.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

// Sentinel errors for connection and protocol failure classification.
// The HTTP client wraps net/http errors with these so the error handler
// can use errors.Is() instead of substring matching.
var (
	ErrConnectionFailed = errors.New("connection failed")
	ErrInvalidURL       = errors.New("invalid URL")
)

// connectionError wraps a sentinel error for errors.Is() classification
// while preserving the original net/http error message for debugging.
type connectionError struct {
	msg      string
	sentinel error
}

func (e *connectionError) Error() string { return e.msg }
func (e *connectionError) Unwrap() error { return e.sentinel }

// NewConnectionError wraps an original error message with a sentinel for
// errors.Is() classification.
func NewConnectionError(msg string, sentinel error) error {
	return &connectionError{msg: msg, sentinel: sentinel}
}

// APIError represents an error response from the API
type APIError struct {
	StatusCode int
	Code       *string
	Family     *string
	Message    *string
	Err        error
}

// Error implements the error interface
func (e *APIError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying error
func (e *APIError) Unwrap() error {
	return e.Err
}

// Is enables errors.Is() matching against sentinel status errors.
// errors.Is(err, api.ErrUnauthorized) returns true when the APIError has status 401.
func (e *APIError) Is(target error) bool {
	switch e.StatusCode {
	case 401:
		return target == ErrUnauthorized
	case 403:
		return target == ErrForbidden
	default:
		return false
	}
}

var _ error = &APIError{}

// Setup errors represent bugs in the configuration of the client

type SetupError struct {
	Message string
	// Err is the underlying cause, when there is one (for example an auth sentinel).
	Err error
}

// Error implements the error interface
func (e *SetupError) Error() string {
	return e.Message
}

func NewSetupError(message string) *SetupError {
	return &SetupError{Message: message}
}

// Unwrap returns the underlying cause so errors.Is matches it.
func (e *SetupError) Unwrap() error { return e.Err }

// NewSetupErrorWrapping keeps cause for errors.Is/As; the user-facing message is cause's text.
func NewSetupErrorWrapping(cause error) *SetupError {
	return &SetupError{Message: cause.Error(), Err: cause}
}

var _ error = &SetupError{}
