package main

import (
	"fmt"
	"io"
	"os"

	"github.com/hwells4/ap/internal/output"
)

const version = "0.1.0"

// cliDeps holds injectable dependencies for testability.
type cliDeps struct {
	mode   output.Mode
	stdout io.Writer
	stderr io.Writer
	getwd  func() (string, error)
}

func main() {
	exitCode := run(os.Args[1:])
	os.Exit(exitCode)
}

func run(args []string) int {
	mode := output.DetectMode(output.DetectOptions{
		JSONFlag:    hasJSONFlag(args),
		StdoutIsTTY: output.IsTerminalStdout(),
		Env:         envMap(),
	})

	return runWithDeps(args, cliDeps{
		mode:   mode,
		stdout: os.Stdout,
		stderr: os.Stderr,
		getwd:  os.Getwd,
	})
}

func runWithDeps(args []string, deps cliDeps) int {
	if len(args) == 0 {
		rendered, err := output.RenderNoArgs(deps.mode, version)
		if err != nil {
			_, _ = fmt.Fprintln(deps.stderr, err)
			return output.ExitGeneralError
		}
		_, _ = fmt.Fprintln(deps.stdout, rendered)
		return output.ExitSuccess
	}

	switch args[0] {
	case "list":
		return runList(args[1:], deps)
	}

	return renderError(deps, output.ExitInvalidArgs, output.NewError(
		"UNKNOWN_COMMAND",
		fmt.Sprintf("unknown command %q", args[0]),
		"Supported commands: list. More coming in later milestones.",
		"ap <command> [args] [flags]",
		[]string{
			"ap",
			"ap run <spec> <session>",
			"ap list",
		},
	))
}

// renderError writes a structured error in the appropriate mode and returns the exit code.
func renderError(deps cliDeps, exitCode int, errResp output.ErrorResponse) int {
	if deps.mode == output.ModeJSON {
		serialized, err := output.MarshalError(errResp)
		if err != nil {
			_, _ = fmt.Fprintln(deps.stderr, err)
			return output.ExitGeneralError
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return exitCode
	}

	_, _ = fmt.Fprintf(deps.stderr, "%s: %s\n", errResp.Error.Code, errResp.Error.Message)
	if errResp.Error.Detail != "" {
		_, _ = fmt.Fprintln(deps.stderr, errResp.Error.Detail)
	}
	if errResp.Error.Syntax != "" {
		_, _ = fmt.Fprintf(deps.stderr, "syntax: %s\n", errResp.Error.Syntax)
	}
	for _, suggestion := range errResp.Error.Suggestions {
		_, _ = fmt.Fprintf(deps.stderr, "try: %s\n", suggestion)
	}
	return exitCode
}

func hasJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func envMap() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return env
}
