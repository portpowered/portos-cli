package auth

import "errors"

// Sentinel errors for auth failure classification.
// The error handler uses errors.Is() against these to identify auth failures
// without relying on substring matching of error messages.
var (
	ErrNoCredentials = errors.New("no credentials configured")
	ErrTokenExpired  = errors.New("token expired")
	ErrTokenInvalid  = errors.New("token invalid")
)

// authError wraps a sentinel error for errors.Is() classification while
// keeping the user-facing message unchanged (no sentinel text appended).
type authError struct {
	msg      string
	sentinel error
}

func (e *authError) Error() string { return e.msg }
func (e *authError) Unwrap() error { return e.sentinel }

func newAuthError(msg string, sentinel error) error {
	return &authError{msg: msg, sentinel: sentinel}
}
