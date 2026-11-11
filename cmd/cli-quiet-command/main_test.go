package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRun_PrintsCompactSuccessAndSuppressesSuccessfulOutput(t *testing.T) {
	commandPath := writeCLIQuietFakeCommand(t, []string{
		"?   github.com/portpowered/portos-cli/internal/api [no test files]",
		"ok  github.com/portpowered/portos-cli/internal/cli 0.123s",
	}, 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--label", "cli:test", "--success", "CLI tests passed.", "--", commandPath}, &stdout, &stderr, nil)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}

	if got := stdout.String(); got != "[cli:test] CLI tests passed.\n" {
		t.Fatalf("stdout = %q", got)
	}
	if strings.Contains(stdout.String(), "github.com/portpowered") {
		t.Fatalf("expected successful command output to be suppressed, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRun_FlushesFailureDiagnosticsAndExitCode(t *testing.T) {
	cwd := t.TempDir()
	commandPath := writeCLIQuietFakeCommand(t, []string{
		"--- FAIL: TestBrokenCLI",
		"cli/internal/example",
	}, 6)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--label", "cli:test", "--success", "CLI tests passed.", "--cwd", cwd, "--", commandPath}, &stdout, &stderr, nil)
	if exitCode != 6 {
		t.Fatalf("exit code = %d, want 6", exitCode)
	}

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	output := stderr.String()
	for _, want := range []string{
		"[cli:test] command failed.",
		"Command: " + commandPath,
		"Working directory: " + cwd,
		"--- FAIL: TestBrokenCLI",
		"cli/internal/example",
		"Exit code: 6",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in failure output:\n%s", want, output)
		}
	}
}

func TestRun_PortosVerboseInheritsCommandOutput(t *testing.T) {
	commandPath := writeCLIQuietFakeCommand(t, []string{
		"verbose cli build details",
	}, 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--label", "cli:build", "--success", "CLI binary built.", "--", commandPath}, &stdout, &stderr, []string{"PORTOS_VERBOSE=1"})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}

	if got := stdout.String(); !strings.Contains(got, "verbose cli build details") {
		t.Fatalf("expected verbose command output, got:\n%s", got)
	}
	if strings.Contains(stdout.String(), "CLI binary built.") {
		t.Fatalf("expected no compact summary in verbose mode, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestIsPortosVerboseOnlyAcceptsOne(t *testing.T) {
	if !isPortosVerbose([]string{"PORTOS_VERBOSE=1"}) {
		t.Fatal("expected PORTOS_VERBOSE=1 to enable verbose mode")
	}
	if isPortosVerbose([]string{"PORTOS_VERBOSE=true"}) {
		t.Fatal("expected PORTOS_VERBOSE=true to remain quiet mode")
	}
	if isPortosVerbose([]string{"OTHER=1"}) {
		t.Fatal("expected missing PORTOS_VERBOSE to remain quiet mode")
	}
}

func writeCLIQuietFakeCommand(t *testing.T, outputLines []string, exitCode int) string {
	t.Helper()

	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		commandPath := filepath.Join(dir, "fake-command.bat")
		lines := []string{"@echo off"}
		for _, line := range outputLines {
			lines = append(lines, "echo "+line)
		}
		lines = append(lines, "exit /b "+fmt.Sprint(exitCode))
		if err := os.WriteFile(commandPath, []byte(strings.Join(lines, "\r\n")), 0o600); err != nil {
			t.Fatalf("write fake command: %v", err)
		}
		return commandPath
	}

	commandPath := filepath.Join(dir, "fake-command")
	lines := []string{"#!/bin/sh"}
	for _, line := range outputLines {
		lines = append(lines, "printf '%s\\n' "+shellSingleQuote(line))
	}
	lines = append(lines, fmt.Sprintf("exit %d", exitCode))
	if err := os.WriteFile(commandPath, []byte(strings.Join(lines, "\n")), 0o700); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	return commandPath
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
