package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/spec"
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
	case "run":
		return runRun(args[1:], deps)
	}

	return renderError(deps, output.ExitInvalidArgs, output.NewError(
		"UNKNOWN_COMMAND",
		fmt.Sprintf("unknown command %q", args[0]),
		"Supported commands in this milestone slice: run, list.",
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

type runRequest struct {
	SpecRaw     string
	Session     string
	Iterations  *int
	Provider    string
	Model       string
	InputFiles  []string
	Context     string
	Force       bool
	Foreground  bool
	ExplainSpec bool
}

func runRun(args []string, deps cliDeps) int {
	req, parsedSpec, errResp := parseRunArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	payload := map[string]any{
		"request":     serializeRunRequest(req),
		"parsed_spec": summarizeSpec(parsedSpec),
	}
	if req.ExplainSpec {
		payload["explain_spec"] = true
		payload["foreground"] = req.Foreground
	}

	if deps.mode == output.ModeJSON {
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, nil))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render run output",
				err.Error(),
				"ap run <spec> <session> [flags]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if req.ExplainSpec {
		_, _ = fmt.Fprintln(deps.stdout, "Spec explain generated.")
		return output.ExitSuccess
	}

	_, _ = fmt.Fprintf(deps.stdout, "Parsed run request for session %q.\n", req.Session)
	_, _ = fmt.Fprintln(deps.stdout, "Execution wiring lands in a later milestone.")
	return output.ExitSuccess
}

func parseRunArgs(args []string) (runRequest, spec.Spec, *output.ErrorResponse) {
	req := runRequest{}
	positional := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--json":
			// Global flag handled via output mode; accepted here for command-level parsing.
			continue
		case arg == "--explain-spec":
			req.ExplainSpec = true
		case arg == "-f" || arg == "--force":
			req.Force = true
		case arg == "--fg" || arg == "--foreground":
			req.Foreground = true
		case arg == "-n" || strings.HasPrefix(arg, "-n="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			parsed, convErr := strconv.Atoi(value)
			if convErr != nil || parsed <= 0 {
				return runParseError(fmt.Sprintf("flag %q requires a positive integer, got %q", "-n", value), "")
			}
			req.Iterations = &parsed
		case arg == "--provider" || strings.HasPrefix(arg, "--provider="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			req.Provider = strings.TrimSpace(value)
		case arg == "-m" || arg == "--model" || strings.HasPrefix(arg, "-m=") || strings.HasPrefix(arg, "--model="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			req.Model = strings.TrimSpace(value)
		case arg == "-i" || arg == "--input" || strings.HasPrefix(arg, "-i=") || strings.HasPrefix(arg, "--input="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			req.InputFiles = append(req.InputFiles, strings.TrimSpace(value))
		case arg == "-c" || arg == "--context" || strings.HasPrefix(arg, "-c=") || strings.HasPrefix(arg, "--context="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			req.Context = value
		case strings.HasPrefix(arg, "-"):
			return runParseError(
				fmt.Sprintf("unknown flag %q", arg),
				"Supported flags: -n, --provider, -m/--model, -i/--input, -c/--context, -f/--force, --fg, --json, --explain-spec",
			)
		default:
			positional = append(positional, arg)
		}
	}

	if len(positional) == 0 {
		return runParseError("missing required arguments: <spec> and <session>", "ap run requires a spec and session name.")
	}
	if len(positional) == 1 {
		return runParseError("missing required argument: <session>", fmt.Sprintf("Got spec %q but no session name.", positional[0]))
	}
	if len(positional) > 2 {
		return runParseError(fmt.Sprintf("expected 2 positional arguments, got %d", len(positional)), fmt.Sprintf("Extra arguments: %s", strings.Join(positional[2:], ", ")))
	}

	req.SpecRaw = strings.TrimSpace(positional[0])
	req.Session = strings.TrimSpace(positional[1])
	if req.SpecRaw == "" || req.Session == "" {
		return runParseError("run requires non-empty <spec> and <session>", "")
	}

	parsedSpec, err := spec.Parse(req.SpecRaw)
	if err != nil {
		code := "INVALID_SPEC"
		if strings.Contains(err.Error(), "file not found") {
			code = "FILE_NOT_FOUND"
		} else if strings.Contains(err.Error(), "stage not found") {
			code = "STAGE_NOT_FOUND"
		}
		errResp := output.NewError(
			code,
			err.Error(),
			err.Error(),
			"ap run <spec> <session> [flags]",
			[]string{
				"ap run ralph my-session",
				"ap run ralph:25 my-session",
				"ap run ./prompt.md my-session",
			},
		)
		return runRequest{}, nil, &errResp
	}

	return req, parsedSpec, nil
}

func runParseError(message, detail string) (runRequest, spec.Spec, *output.ErrorResponse) {
	errResp := output.NewError(
		"INVALID_ARGUMENT",
		message,
		detail,
		"ap run <spec> <session> [-n COUNT] [--provider NAME] [-m MODEL] [-i INPUT...] [-c CONTEXT] [-f] [--fg] [--explain-spec] [--json]",
		[]string{
			"ap run ralph my-session",
			"ap run ralph:25 my-session -n 10",
			"ap run --explain-spec ./prompt.md my-session",
		},
	)
	return runRequest{}, nil, &errResp
}

func readFlagValue(arg string, args []string, index int) (value string, nextIndex int, err error) {
	if eq := strings.Index(arg, "="); eq >= 0 {
		return arg[eq+1:], index, nil
	}
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("flag %q requires a value", arg)
	}
	return args[index+1], index + 1, nil
}

func serializeRunRequest(req runRequest) map[string]any {
	out := map[string]any{
		"spec":         req.SpecRaw,
		"session":      req.Session,
		"force":        req.Force,
		"foreground":   req.Foreground,
		"explain_spec": req.ExplainSpec,
	}
	if req.Iterations != nil {
		out["iterations"] = *req.Iterations
	}
	if req.Provider != "" {
		out["provider"] = req.Provider
	}
	if req.Model != "" {
		out["model"] = req.Model
	}
	if req.Context != "" {
		out["context"] = req.Context
	}
	if len(req.InputFiles) > 0 {
		out["input"] = req.InputFiles
	}
	return out
}

func summarizeSpec(parsed spec.Spec) map[string]any {
	summary := map[string]any{
		"raw":  parsed.Raw(),
		"kind": specKindName(parsed.Kind()),
	}
	switch s := parsed.(type) {
	case spec.StageSpec:
		summary["name"] = s.Name
		summary["iterations"] = s.Iterations
	case spec.FileSpec:
		summary["path"] = s.Path
		summary["file_kind"] = specKindName(s.FileKind)
	case spec.ChainSpec:
		stages := make([]map[string]any, 0, len(s.Stages))
		for _, stageSpec := range s.Stages {
			stages = append(stages, map[string]any{
				"name":       stageSpec.Name,
				"iterations": stageSpec.Iterations,
			})
		}
		summary["stages"] = stages
	}
	return summary
}

func specKindName(kind spec.SpecKind) string {
	switch kind {
	case spec.KindStage:
		return "stage"
	case spec.KindFilePrompt:
		return "prompt_file"
	case spec.KindFileYAML:
		return "yaml_file"
	case spec.KindChain:
		return "chain"
	default:
		return "unknown"
	}
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
