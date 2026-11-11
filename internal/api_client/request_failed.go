package api

import (
	"fmt"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
)

// MessageRequestFailed reports a message request the server stored but that ended
// FAILED or PARTIALLY_SUCCEEDED. It keeps the whole request so callers can branch on
// the typed errors instead of parsing the message.
type MessageRequestFailed struct {
	Request *api.MessageRequest
}

// Error summarizes the request and the first failure it recorded.
func (e *MessageRequestFailed) Error() string {
	if e.Request == nil {
		return "message request failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "message request %s ended %s", e.Request.Id, e.Request.Status)
	if len(e.Request.Errors) > 0 {
		first := e.Request.Errors[0]
		if first.Error != nil {
			fmt.Fprintf(&b, ": %s", first.Error.Message)
			if first.Error.Details != nil && first.Error.Details.ProviderCode != nil {
				fmt.Fprintf(&b, " (%s)", *first.Error.Details.ProviderCode)
			}
		}
		if len(e.Request.Errors) > 1 {
			fmt.Fprintf(&b, "; %d more errors", len(e.Request.Errors)-1)
		}
	}
	b.WriteString("; inspect its results and errors before another command")
	return b.String()
}

var _ error = &MessageRequestFailed{}
