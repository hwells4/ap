package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/fuzzy"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/runtarget"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/spec"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/internal/store"
	"github.com/hwells4/ap/pkg/provider"
)

const version = "0.1.0"

type cliDeps struct {
	mode        output.Mode
	stdout      io.Writer
	stderr      io.Writer
	getwd       func() (string, error)
	corrections []output.Correction
	launcher    session.Launcher
	store       *store.Store
	config      *config.Config // nil = load from default path
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

// openStore opens the SQLite store at .ap/ap.db relative to the project root.
// When createIfMissing=false, it returns (nil, nil) if no local DB exists.
// The caller must close the store when done.
func openStore(deps cliDeps, createIfMissing bool) (*store.Store, error) {
	projectRoot := "."
	if deps.getwd != nil {
		if cwd, err := deps.getwd(); err == nil {
			projectRoot = cwd
		}
	}
	dbPath := filepath.Join(projectRoot, ".ap", "ap.db")
	if !createIfMissing {
		if _, err := os.Stat(dbPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
	}
	return store.Open(dbPath)
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
	if args[0] == "_run" {
		return runInternalRun(args[1:], deps)
	}

	commandName, commandCorrection, ok := fuzzy.NormalizeCommand(args[0])
	if !ok {
		suggested := fuzzy.SuggestCommands(args[0], 3)
		suggestions := make([]string, 0, len(suggested)+1)
		for _, cmd := range suggested {
			suggestions = append(suggestions, "ap "+cmd)
		}
		suggestions = append(suggestions, "ap")
		errResp := output.NewError(
			"UNKNOWN_COMMAND",
			fmt.Sprintf("unknown command %q", args[0]),
			"Supported commands: run, list, status, resume, kill, logs, clean, watch, query.",
			"ap <command> [args] [flags]",
			suggestions,
		)
		errResp.Error.Available = map[string]any{"commands": output.Commands()}
		return renderError(deps, output.ExitInvalidArgs, errResp)
	}
	if commandCorrection != nil {
		deps.corrections = append(deps.corrections, output.Correction{
			From: commandCorrection.From, To: commandCorrection.To, Hint: commandCorrection.Hint,
		})
	}

	// Open store for commands that query session state from the current project.
	// `run` resolves project root after parsing flags, so it opens its store later.
	if commandName != "run" && deps.store == nil {
		createIfMissing := commandName == "clean"
		s, err := openStore(deps, createIfMissing)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "failed to open store: %v\n", err)
			return output.ExitGeneralError
		}
		if s != nil {
			defer s.Close()
			deps.store = s
		}
	}

	switch commandName {
	case "list":
		return runList(args[1:], deps)
	case "run":
		return runRun(args[1:], deps)
	case "kill":
		return runKill(args[1:], deps)
	case "status":
		return runStatus(args[1:], deps)
	case "resume":
		return runResume(args[1:], deps)
	case "logs":
		return runLogs(args[1:], deps)
	case "clean":
		return runClean(args[1:], deps)
	case "watch":
		return runWatch(args[1:], deps)
	case "query":
		return runQuery(args[1:], deps)
	default:
		_, _ = fmt.Fprintf(deps.stderr, "command %q is not yet implemented\n", commandName)
		return output.ExitGeneralError
	}
}

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
	OnEscalate  string
	ProjectRoot string
	InputFiles  []string
	Context     string
	Force       bool
	Foreground  bool
	ExplainSpec bool
}

// loadConfigOrDefault returns the config from deps (test injection), or loads
// from the default path, falling back to compiled defaults on any error.
func loadConfigOrDefault(deps cliDeps) config.Config {
	if deps.config != nil {
		return *deps.config
	}
	cfg, err := config.Load("")
	if err != nil {
		return config.Default()
	}
	return cfg
}

// resolveDefault returns the first non-empty value in precedence order:
// CLI flag > environment variable > config file value > compiled default.
func resolveDefault(flagVal, envKey, configVal, compiled string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if configVal != "" {
		return configVal
	}
	return compiled
}

func runRun(args []string, deps cliDeps) int {
	req, parsedSpec, corrections, errResp := parseRunArgs(args, deps.getwd)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}
	deps.corrections = append(deps.corrections, corrections...)

	cfg := loadConfigOrDefault(deps)

	// Apply config defaults for provider and model when not set by flags.
	req.Provider = resolveDefault(req.Provider, "AP_PROVIDER", cfg.Defaults.Provider, "claude")
	req.Model = resolveDefault(req.Model, "AP_MODEL", cfg.Defaults.Model, "")

	// Validate and normalize provider name.
	if req.Provider != "" {
		if provErr := validateProviderName(req.Provider); provErr != nil {
			return renderError(deps, output.ExitInvalidArgs, *provErr)
		}
		req.Provider = provider.NormalizeName(req.Provider)
	}

	// Validate model for the selected provider.
	if req.Model != "" {
		provName := req.Provider
		if provName == "" {
			provName = "claude"
		}
		if modelErr := validateModelForProvider(req.Model, provName); modelErr != nil {
			return renderError(deps, output.ExitInvalidArgs, *modelErr)
		}
	}

	// Explain-spec mode: just return the parsed spec without launching.
	if req.ExplainSpec {
		payload := map[string]any{
			"request":      serializeRunRequest(req),
			"parsed_spec":  summarizeSpec(parsedSpec),
			"explain_spec": true,
			"foreground":   req.Foreground,
		}
		if deps.mode == output.ModeJSON {
			serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
			if err != nil {
				return renderError(deps, output.ExitGeneralError, output.NewError(
					"GENERAL_ERROR", "failed to render run output", err.Error(),
					"ap run <spec> <session> [flags]", nil))
			}
			_, _ = fmt.Fprintln(deps.stdout, string(serialized))
			return output.ExitSuccess
		}
		_, _ = fmt.Fprintln(deps.stdout, "Spec explain generated.")
		return output.ExitSuccess
	}

	projectRoot, rootErr := resolveRunProjectRoot(req.ProjectRoot, deps.getwd)
	if rootErr != nil {
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"INVALID_ARGUMENT",
			rootErr.Error(),
			"",
			"ap run <spec> <session> [flags]",
			[]string{"ap run ralph my-session --project-root /abs/path/to/repo"},
		))
	}
	req.ProjectRoot = projectRoot

	// Open the store at the resolved project root for foreground polling and
	// local session metadata lookups.
	if deps.store == nil {
		s, err := store.Open(filepath.Join(projectRoot, ".ap", "ap.db"))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"STORE_OPEN_FAILED",
				fmt.Sprintf("failed to open session store at %q", filepath.Join(projectRoot, ".ap", "ap.db")),
				err.Error(),
				"ap run <spec> <session> [flags]",
				nil,
			))
		}
		defer s.Close()
		deps.store = s
	}

	target, targetErr := runtarget.Resolve(projectRoot, runtarget.SourceCLI)
	if targetErr != nil {
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"INVALID_ARGUMENT",
			targetErr.Error(),
			"",
			"ap run <spec> <session> [flags]",
			nil,
		))
	}

	// Resolve launcher: use injected launcher, then config/env/compiled default.
	launcher := deps.launcher
	if launcher == nil {
		launcherName := resolveDefault("", "AP_LAUNCHER", cfg.Defaults.Launcher, "tmux")
		var err error
		launcher, err = session.ResolveLauncher(launcherName)
		if err != nil {
			launcher = session.NewTmuxLauncher()
		}
	}

	// Foreground mode: run directly in this process.
	if req.Foreground {
		return runForeground(req, parsedSpec, target, launcher, deps)
	}

	// Background mode: launch via tmux.
	if !launcher.Available() {
		// Fall back to foreground if launcher is not available.
		return runForeground(req, parsedSpec, target, launcher, deps)
	}

	sess, err := session.Start(parsedSpec, req.Session, session.StartOpts{
		ProjectRoot:  projectRoot,
		RunTarget:    target,
		TargetSource: target.Source,
		Provider:     req.Provider,
		Model:        req.Model,
		OnEscalate:   req.OnEscalate,
		Context:      req.Context,
		InputFiles:   req.InputFiles,
		Force:        req.Force,
		Launcher:     launcher,
	})
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"START_FAILED",
			fmt.Sprintf("failed to start session: %v", err),
			"",
			"ap run <spec> <session> [flags]",
			[]string{"ap list", "ap run ralph:3 my-session --fg"},
		))
	}

	payload := map[string]any{
		"session":       sess.Name,
		"run_dir":       sess.RunDir,
		"project_root":  target.ProjectRoot,
		"repo_root":     target.RepoRoot,
		"config_root":   target.ConfigRoot,
		"project_key":   target.ProjectKey,
		"target_source": target.Source,
		"request":       serializeRunRequest(req),
		"parsed_spec":   summarizeSpec(parsedSpec),
		"launched":      true,
		"launcher":      launcher.Name(),
	}
	if deps.mode == output.ModeJSON {
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR", "failed to render run output", err.Error(),
				"ap run <spec> <session> [flags]", nil))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}
	_, _ = fmt.Fprintf(deps.stdout, "Session %q started in tmux.\n", sess.Name)
	_, _ = fmt.Fprintf(deps.stdout, "  ap status %s\n", sess.Name)
	_, _ = fmt.Fprintf(deps.stdout, "  ap logs %s -f\n", sess.Name)
	return output.ExitSuccess
}

// runForeground runs the session directly in the current process using ap _run.
func runForeground(req runRequest, parsedSpec spec.Spec, target runtarget.Target, launcher session.Launcher, deps cliDeps) int {
	if !launcher.Available() {
		_, _ = fmt.Fprintln(deps.stderr, "launcher is not available for session execution")
		return output.ExitGeneralError
	}

	sess, err := session.Start(parsedSpec, req.Session, session.StartOpts{
		ProjectRoot:  target.ProjectRoot,
		RunTarget:    target,
		TargetSource: target.Source,
		Provider:     req.Provider,
		Model:        req.Model,
		OnEscalate:   req.OnEscalate,
		Context:      req.Context,
		InputFiles:   req.InputFiles,
		Force:        req.Force,
		Launcher:     launcher,
	})
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"START_FAILED",
			fmt.Sprintf("failed to start session: %v", err),
			"",
			"ap run <spec> <session> --fg [flags]",
			[]string{"ap list"},
		))
	}

	_, _ = fmt.Fprintf(deps.stderr, "Session %q running (foreground wait)...\n", sess.Name)

	// Poll store until session completes or timeout (30 minutes).
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Minute)
	for {
		if time.Now().After(deadline) {
			_, _ = fmt.Fprintln(deps.stderr, "foreground wait timed out (30m)")
			return output.ExitGeneralError
		}
		time.Sleep(2 * time.Second)
		if deps.store == nil {
			continue
		}
		row, err := deps.store.GetSession(ctx, sess.Name)
		if err != nil {
			continue
		}
		status := row.Status
		if status == "completed" || status == "failed" || status == "paused" {
			payload := map[string]any{
				"session":             sess.Name,
				"run_dir":             sess.RunDir,
				"status":              status,
				"iteration_completed": row.IterationCompleted,
			}
			if deps.mode == output.ModeJSON {
				serialized, _ := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
				_, _ = fmt.Fprintln(deps.stdout, string(serialized))
			} else {
				_, _ = fmt.Fprintf(deps.stdout, "Session %q: %s (%d iterations)\n", sess.Name, status, row.IterationCompleted)
			}
			if status == "failed" {
				return output.ExitProviderError
			}
			return output.ExitSuccess
		}
	}
}

func parseRunArgs(args []string, getwd func() (string, error)) (runRequest, spec.Spec, []output.Correction, *output.ErrorResponse) {
	req := runRequest{}
	corrections := []output.Correction{}
	positional := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue
		case arg == "--explain-spec":
			req.ExplainSpec = true
		case arg == "-f" || arg == "--force":
			req.Force = true
		case arg == "--fg" || arg == "--foreground":
			req.Foreground = true
		case arg == "--iterations" || strings.HasPrefix(arg, "--iterations="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			parsed, convErr := strconv.Atoi(value)
			if convErr != nil || parsed <= 0 {
				return runParseError(fmt.Sprintf("flag %q requires a positive integer, got %q", "--iterations", value), "")
			}
			req.Iterations = &parsed
			corrections = append(corrections, output.Correction{From: "--iterations", To: "-n", Hint: "flag alias normalized"})
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
			normalized, providerCorrection := fuzzy.NormalizeProvider(value)
			req.Provider = strings.TrimSpace(normalized)
			if providerCorrection != nil {
				corrections = append(corrections, toOutputCorrection(*providerCorrection))
			}
		case arg == "-m" || arg == "--model" || strings.HasPrefix(arg, "-m=") || strings.HasPrefix(arg, "--model="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			req.Model = strings.TrimSpace(value)
		case arg == "--on-escalate" || strings.HasPrefix(arg, "--on-escalate="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			req.OnEscalate = strings.TrimSpace(value)
		case arg == "--project-root" || strings.HasPrefix(arg, "--project-root=") || arg == "--workdir" || strings.HasPrefix(arg, "--workdir="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return runParseError(err.Error(), "")
			}
			i = next
			req.ProjectRoot = strings.TrimSpace(value)
			if strings.HasPrefix(arg, "--workdir") {
				corrections = append(corrections, output.Correction{
					From: "--workdir",
					To:   "--project-root",
					Hint: "flag alias normalized",
				})
			}
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
			return runParseError(fmt.Sprintf("unknown flag %q", arg),
				"Supported flags: -n, --iterations, --provider, -m/--model, --on-escalate, --project-root, -i/--input, -c/--context, -f/--force, --fg, --json, --explain-spec")
		default:
			positional = append(positional, arg)
		}
	}
	positional, positionalCorrections := recoverPositionalSpec(positional)
	corrections = append(corrections, positionalCorrections...)
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
	if req.OnEscalate != "" {
		if _, err := parseOnEscalateOverride(req.OnEscalate); err != nil {
			return runParseError(fmt.Sprintf("invalid --on-escalate value %q: %v", req.OnEscalate, err), "")
		}
	}
	stageProjectRoot := strings.TrimSpace(req.ProjectRoot)
	if stageProjectRoot == "" && getwd != nil {
		if cwd, err := getwd(); err == nil {
			stageProjectRoot = cwd
		}
	}
	if stageProjectRoot != "" {
		abs, err := filepath.Abs(stageProjectRoot)
		if err != nil {
			return runParseError(fmt.Sprintf("invalid --project-root value %q: %v", req.ProjectRoot, err), "")
		}
		info, err := os.Stat(abs)
		if err != nil {
			return runParseError(fmt.Sprintf("invalid --project-root value %q: %v", req.ProjectRoot, err), "")
		}
		if !info.IsDir() {
			return runParseError(fmt.Sprintf("invalid --project-root value %q: not a directory", req.ProjectRoot), "")
		}
		stageProjectRoot = abs
		if req.ProjectRoot != "" {
			req.ProjectRoot = abs
		}
	}

	parsedSpec, err := spec.ParseWithOptions(req.SpecRaw, spec.ParseOptions{
		StageResolveOpts: stage.ResolveOptions{ProjectRoot: stageProjectRoot},
	})
	if err != nil {
		availableStages := discoverAvailableStages(getwd, stageProjectRoot)
		recoveredSpec, recoveredCorrections, recoveredParsed, recovered := recoverStageSpec(req.SpecRaw, availableStages)
		if recovered {
			req.SpecRaw = recoveredSpec
			corrections = append(corrections, recoveredCorrections...)
			return req, recoveredParsed, corrections, nil
		}
		code := "INVALID_SPEC"
		if strings.Contains(err.Error(), "file not found") {
			code = "FILE_NOT_FOUND"
		} else if strings.Contains(err.Error(), "stage not found") {
			return runStageNotFoundError(req, err, availableStages)
		}
		errResp := output.NewError(code, err.Error(), err.Error(), "ap run <spec> <session> [flags]",
			[]string{"ap run ralph my-session", "ap run ralph:25 my-session", "ap run ./prompt.md my-session"})
		return runRequest{}, nil, nil, &errResp
	}
	return req, parsedSpec, corrections, nil
}

func runParseError(message, detail string) (runRequest, spec.Spec, []output.Correction, *output.ErrorResponse) {
	errResp := output.NewError("INVALID_ARGUMENT", message, detail,
		"ap run <spec> <session> [-n COUNT] [--provider NAME] [-m MODEL] [--on-escalate HANDLER] [--project-root DIR] [-i INPUT...] [-c CONTEXT] [-f] [--fg] [--explain-spec] [--json]",
		[]string{"ap run ralph my-session", "ap run ralph:25 my-session -n 10", "ap run --explain-spec ./prompt.md my-session"})
	return runRequest{}, nil, nil, &errResp
}

func toOutputCorrection(c fuzzy.Correction) output.Correction {
	return output.Correction{From: c.From, To: c.To, Hint: c.Hint}
}

func recoverPositionalSpec(positional []string) ([]string, []output.Correction) {
	if len(positional) == 3 {
		if _, err := strconv.Atoi(positional[1]); err == nil {
			recoveredSpec := strings.TrimSpace(positional[0]) + ":" + strings.TrimSpace(positional[1])
			return []string{recoveredSpec, positional[2]}, []output.Correction{{From: positional[0] + " " + positional[1], To: recoveredSpec, Hint: "recovered stage iteration shorthand"}}
		}
	}
	if len(positional) == 2 {
		first := strings.TrimSpace(positional[0])
		second := strings.TrimSpace(positional[1])
		if _, firstErr := spec.Parse(first); firstErr != nil {
			if _, secondErr := spec.Parse(second); secondErr == nil {
				return []string{second, first}, []output.Correction{{From: first + " " + second, To: second + " " + first, Hint: "recovered <spec> <session> argument order"}}
			}
		}
	}
	return positional, nil
}

func discoverAvailableStages(getwd func() (string, error), projectRootOverride string) []string {
	names := map[string]struct{}{}
	builtinNames, err := stage.BuiltinStageNames()
	if err == nil {
		for _, name := range builtinNames {
			if name = strings.TrimSpace(name); name != "" {
				names[name] = struct{}{}
			}
		}
	}
	projectRoot := strings.TrimSpace(projectRootOverride)
	if projectRoot == "" && getwd != nil {
		if cwd, err := getwd(); err == nil {
			projectRoot = cwd
		}
	}
	if projectRoot != "" {
		entries, err := os.ReadDir(filepath.Join(projectRoot, ".ap", "stages"))
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					if name := strings.TrimSpace(entry.Name()); name != "" {
						names[name] = struct{}{}
					}
				}
			}
		}
	}
	// User-global stages: ~/.config/ap/stages/{name}/
	if home, err := os.UserHomeDir(); err == nil {
		entries, err := os.ReadDir(filepath.Join(home, ".config", "ap", "stages"))
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					if name := strings.TrimSpace(entry.Name()); name != "" {
						names[name] = struct{}{}
					}
				}
			}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func recoverStageSpec(rawSpec string, availableStages []string) (string, []output.Correction, spec.Spec, bool) {
	parsedRaw, err := spec.ParseWithOptions(rawSpec, spec.ParseOptions{SkipStageLookup: true})
	if err != nil {
		return "", nil, nil, false
	}
	switch parsed := parsedRaw.(type) {
	case spec.StageSpec:
		normalizedName, correction, ok := fuzzy.NormalizeStage(parsed.Name, availableStages)
		if !ok || correction == nil || normalizedName == parsed.Name {
			return "", nil, nil, false
		}
		correctedSpec := normalizedName
		if parsed.Iterations > 0 {
			correctedSpec = fmt.Sprintf("%s:%d", normalizedName, parsed.Iterations)
		}
		resolved, err := spec.Parse(correctedSpec)
		if err != nil {
			return "", nil, nil, false
		}
		return correctedSpec, []output.Correction{toOutputCorrection(*correction)}, resolved, true
	case spec.ChainSpec:
		updated := false
		stageParts := make([]string, 0, len(parsed.Stages))
		corrections := make([]output.Correction, 0)
		for _, ss := range parsed.Stages {
			partName := ss.Name
			normalizedName, correction, ok := fuzzy.NormalizeStage(ss.Name, availableStages)
			if ok && correction != nil && normalizedName != ss.Name {
				partName = normalizedName
				corrections = append(corrections, toOutputCorrection(*correction))
				updated = true
			}
			if ss.Iterations > 0 {
				stageParts = append(stageParts, fmt.Sprintf("%s:%d", partName, ss.Iterations))
			} else {
				stageParts = append(stageParts, partName)
			}
		}
		if !updated {
			return "", nil, nil, false
		}
		correctedSpec := strings.Join(stageParts, " -> ")
		resolved, err := spec.Parse(correctedSpec)
		if err != nil {
			return "", nil, nil, false
		}
		return correctedSpec, corrections, resolved, true
	default:
		return "", nil, nil, false
	}
}

func runStageNotFoundError(req runRequest, parseErr error, availableStages []string) (runRequest, spec.Spec, []output.Correction, *output.ErrorResponse) {
	missingStage := extractMissingStageName(parseErr.Error())
	stageSuggestions := fuzzy.SuggestStages(missingStage, availableStages, 3)
	suggestions := make([]string, 0, len(stageSuggestions)+1)
	for _, stageName := range stageSuggestions {
		suggestions = append(suggestions, fmt.Sprintf("ap run %s %s", stageName, req.Session))
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "ap list")
	}
	errResp := output.NewError("STAGE_NOT_FOUND", parseErr.Error(), parseErr.Error(), "ap run <spec> <session> [flags]", suggestions)
	if len(availableStages) > 0 {
		errResp.Error.Available = map[string]any{"stages": availableStages}
	}
	return runRequest{}, nil, nil, &errResp
}

func extractMissingStageName(errMessage string) string {
	const marker = "stage not found: \""
	start := strings.Index(errMessage, marker)
	if start == -1 {
		return ""
	}
	start += len(marker)
	end := strings.Index(errMessage[start:], "\"")
	if end == -1 {
		return ""
	}
	return errMessage[start : start+end]
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

func resolveRunProjectRoot(projectRootFlag string, getwd func() (string, error)) (string, error) {
	projectRootFlag = strings.TrimSpace(projectRootFlag)
	if projectRootFlag == "" {
		if getwd == nil {
			return "", fmt.Errorf("unable to determine current working directory")
		}
		cwd, err := getwd()
		if err != nil {
			return "", fmt.Errorf("unable to determine current working directory: %w", err)
		}
		projectRootFlag = cwd
	}
	abs, err := filepath.Abs(projectRootFlag)
	if err != nil {
		return "", fmt.Errorf("invalid --project-root value %q: %w", projectRootFlag, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("invalid --project-root value %q: %w", projectRootFlag, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("invalid --project-root value %q: not a directory", projectRootFlag)
	}
	return abs, nil
}

func serializeRunRequest(req runRequest) map[string]any {
	out := map[string]any{"spec": req.SpecRaw, "session": req.Session, "force": req.Force, "foreground": req.Foreground, "explain_spec": req.ExplainSpec}
	if req.Iterations != nil {
		out["iterations"] = *req.Iterations
	}
	if req.Provider != "" {
		out["provider"] = req.Provider
	}
	if req.Model != "" {
		out["model"] = req.Model
	}
	if req.OnEscalate != "" {
		out["on_escalate"] = req.OnEscalate
	}
	if req.ProjectRoot != "" {
		out["project_root"] = req.ProjectRoot
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
	summary := map[string]any{"raw": parsed.Raw(), "kind": specKindName(parsed.Kind())}
	switch s := parsed.(type) {
	case spec.StageSpec:
		summary["name"] = s.Name
		summary["iterations"] = s.Iterations
	case spec.FileSpec:
		summary["path"] = s.Path
		summary["file_kind"] = specKindName(s.FileKind)
	case spec.ChainSpec:
		stages := make([]map[string]any, 0, len(s.Stages))
		for _, ss := range s.Stages {
			stages = append(stages, map[string]any{"name": ss.Name, "iterations": ss.Iterations})
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
