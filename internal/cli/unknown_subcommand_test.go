package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestGroupCommands_RejectUnknownSubcommand(t *testing.T) {
	tests := []struct {
		name    string
		command Command
		use     string
	}{
		{"endpoint", NewEndpointCommand(), "endpoint"},
		{"auth", NewAuthCommand(), "auth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.command.Generate()
			cmd.Use = tt.use
			// Add a dummy subcommand so the group has children
			cmd.AddCommand(&cobra.Command{Use: "valid", RunE: func(cmd *cobra.Command, args []string) error { return nil }})

			cmd.SetArgs([]string{"nonexistent"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)

			err := cmd.Execute()
			if err == nil {
				t.Errorf("expected error for unknown subcommand, got nil")
			}
		})
	}
}

func TestGroupCommands_AcceptNoArgs(t *testing.T) {
	tests := []struct {
		name    string
		command Command
		use     string
	}{
		{"endpoint", NewEndpointCommand(), "endpoint"},
		{"auth", NewAuthCommand(), "auth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.command.Generate()
			cmd.Use = tt.use

			cmd.SetArgs([]string{})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)

			err := cmd.Execute()
			if err != nil {
				t.Errorf("expected no error for bare command, got: %v", err)
			}
		})
	}
}

func TestGroupCommands_HelpFlag(t *testing.T) {
	tests := []struct {
		name    string
		command Command
		use     string
	}{
		{"endpoint", NewEndpointCommand(), "endpoint"},
		{"auth", NewAuthCommand(), "auth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.command.Generate()
			cmd.Use = tt.use

			cmd.SetArgs([]string{"--help"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)

			err := cmd.Execute()
			if err != nil {
				t.Errorf("expected no error for --help, got: %v", err)
			}
		})
	}
}
