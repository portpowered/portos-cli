package cli

import (
	"strings"
	"testing"
)

func TestBuildSupportedCommandsSection(t *testing.T) {
	section := buildSupportedCommandsSection()

	t.Run("starts with header", func(t *testing.T) {
		if !strings.HasPrefix(section, "Supported commands:") {
			t.Errorf("expected section to start with 'Supported commands:', got: %s", section[:50])
		}
	})

	t.Run("contains known message types", func(t *testing.T) {
		expected := []string{"turn-on", "turn-off", "set-brightness", "pause", "stop", "discover-routes"}
		for _, name := range expected {
			if !strings.Contains(section, name) {
				t.Errorf("expected section to contain %q", name)
			}
		}
	})

	t.Run("groups by capability", func(t *testing.T) {
		expected := []string{"Power:", "Brightness:", "Media Player:"}
		for _, group := range expected {
			if !strings.Contains(section, group) {
				t.Errorf("expected section to contain group header %q", group)
			}
		}
	})

	t.Run("excludes outbound-only messages", func(t *testing.T) {
		// "route-discovery-report" has Directionality "out" - should be excluded
		if strings.Contains(section, "route-discovery-report") {
			t.Error("expected section to exclude outbound message 'route-discovery-report'")
		}
	})
}

func TestFriendlyNamespaceName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"power", "Power"},
		{"media-player", "Media Player"},
		{"rtc-session", "Rtc Session"},
		{"pet-feeder", "Pet Feeder"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := friendlyNamespaceName(tt.input)
			if got != tt.want {
				t.Errorf("friendlyNamespaceName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
