package cli

import (
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
)

func TestRootCommand_LongDescriptionContainsWorkflowKeywords(t *testing.T) {
	root := NewRootCommand(logging.NewLoggerRef(), &flags.GlobalFlags{})
	cmd := root.Generate()

	long := cmd.Long

	keywords := []string{
		"init",
		"auth",
		"PORTOS_ACCESS_TOKEN",
	}

	for _, kw := range keywords {
		if !strings.Contains(long, kw) {
			t.Errorf("root Long description should contain %q, but it does not.\nLong:\n%s", kw, long)
		}
	}
}
