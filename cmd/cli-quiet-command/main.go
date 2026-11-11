package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const (
	portosVerboseEnv   = "PORTOS_VERBOSE"
	portosVerboseValue = "1"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

func run(args []string, stdout io.Writer, stderr io.Writer, env []string) int {
	flags := flag.NewFlagSet("cli-quiet-command", flag.ContinueOnError)
	flags.SetOutput(stderr)

	label := flags.String("label", "cli", "short package or phase label for output")
	success := flags.String("success", "CLI command passed.", "compact success message")
	cwd := flags.String("cwd", "", "working directory for the command")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	commandArgs := flags.Args()
	if len(commandArgs) > 0 && commandArgs[0] == "--" {
		commandArgs = commandArgs[1:]
	}
	if len(commandArgs) == 0 {
		fmt.Fprintln(stderr, "usage: cli-quiet-command --label <label> --success <message> [--cwd <dir>] -- <command> [args...]")
		return 2
	}

	cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
	cmd.Env = env
	if *cwd != "" {
		cmd.Dir = *cwd
	}

	if isPortosVerbose(env) {
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		return waitForCommand(cmd, stderr)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "[%s] command failed.\n", *label)
		fmt.Fprintf(stderr, "Command: %s\n", strings.Join(commandArgs, " "))
		if *cwd != "" {
			fmt.Fprintf(stderr, "Working directory: %s\n", *cwd)
		}
		if output.Len() > 0 {
			outputBytes := append([]byte(nil), output.Bytes()...)
			fmt.Fprintln(stderr, "Output:")
			_, _ = output.WriteTo(stderr)
			if !bytes.HasSuffix(outputBytes, []byte("\n")) {
				fmt.Fprintln(stderr)
			}
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(stderr, "Exit code: %d\n", exitErr.ExitCode())
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "[%s] %s\n", *label, *success)
	return 0
}

func waitForCommand(cmd *exec.Cmd, stderr io.Writer) int {
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "run command: %v\n", err)
		return 1
	}

	return 0
}

func isPortosVerbose(env []string) bool {
	for _, pair := range env {
		name, value, ok := strings.Cut(pair, "=")
		if ok && name == portosVerboseEnv {
			return value == portosVerboseValue
		}
	}

	return false
}
