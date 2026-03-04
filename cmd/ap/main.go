package main

import (
	"fmt"
	"os"

	"github.com/hwells4/ap/internal/output"
)

const version = "0.1.0"

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

	if len(args) == 0 {
		rendered, err := output.RenderNoArgs(mode, version)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			return output.ExitGeneralError
		}
		_, _ = fmt.Fprintln(os.Stdout, rendered)
		return output.ExitSuccess
	}

	errResp := output.NewError(
		"UNKNOWN_COMMAND",
		fmt.Sprintf("unknown command %q", args[0]),
		"Only no-args help is implemented in this milestone slice.",
		"ap <command> [args] [flags]",
		[]string{
			"ap",
			"ap run <spec> <session>",
			"ap list",
		},
	)

	if mode == output.ModeJSON {
		serialized, err := output.MarshalError(errResp)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			return output.ExitGeneralError
		}
		_, _ = fmt.Fprintln(os.Stdout, string(serialized))
		return output.ExitInvalidArgs
	}

	_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", errResp.Error.Code, errResp.Error.Message)
	if errResp.Error.Detail != "" {
		_, _ = fmt.Fprintln(os.Stderr, errResp.Error.Detail)
	}
	if errResp.Error.Syntax != "" {
		_, _ = fmt.Fprintf(os.Stderr, "syntax: %s\n", errResp.Error.Syntax)
	}
	for _, suggestion := range errResp.Error.Suggestions {
		_, _ = fmt.Fprintf(os.Stderr, "try: %s\n", suggestion)
	}
	return output.ExitInvalidArgs
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
