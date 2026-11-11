package cli

import (
	"strings"
	"testing"
)

func TestEndpointListCommand_UsesGeneratedCommandDocs(t *testing.T) {
	cmd := NewEndpointListCommand(nil).Generate()

	if cmd.Short != "List endpoints" {
		t.Fatalf("Short = %q, want generated summary", cmd.Short)
	}
	if cmd.Long != "List all endpoints accessible to the current user with optional filtering and pagination." {
		t.Fatalf("Long = %q, want generated description", cmd.Long)
	}
	if !strings.Contains(cmd.Example, "port endpoint list --type camera --json") {
		t.Fatalf("Example = %q, want generated camera example", cmd.Example)
	}

	flag := cmd.Flags().Lookup("type")
	if flag == nil {
		t.Fatal("expected generated type flag")
	}
	if flag.Usage != "Filter endpoints by primary type, such as light or camera." {
		t.Fatalf("type flag usage = %q, want generated description", flag.Usage)
	}
}
