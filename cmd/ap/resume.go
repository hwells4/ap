package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/output"
	sessionpkg "github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/store"
)

func runResume(args []string, deps cliDeps) int {
	sessionName, contextOverride, projectRootFlag, errResp := parseResumeArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	ctx := context.Background()
	selectedStore, cleanup, lookupErr := resolveSessionStore(ctx, deps, sessionName, projectRootFlag)
	if lookupErr != nil {
		if errors.Is(lookupErr, errSessionLookupNotFound) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				"No session with that name in local or machine-wide index.",
				"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
				[]string{"ap query sessions --status paused --json", "ap resume my-session --project-root /abs/path --json"},
			))
		}
		var ambiguous *sessionLookupAmbiguousError
		if errors.As(lookupErr, &ambiguous) {
			suggestions := []string{}
			for _, match := range ambiguous.Matches {
				suggestions = append(suggestions, fmt.Sprintf("ap resume %s --project-root %s --json", sessionName, match.ProjectRoot))
				if len(suggestions) >= 3 {
					break
				}
			}
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"SESSION_AMBIGUOUS",
				lookupErr.Error(),
				"Use --project-root to select the project explicitly.",
				"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
				suggestions,
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_READ_FAILED",
			fmt.Sprintf("failed to resolve store for session %q", sessionName),
			lookupErr.Error(),
			"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
			nil,
		))
	}
	defer cleanup()

	// Load session from store.
	row, err := selectedStore.GetSession(ctx, sessionName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				"No session with that name in the store.",
				"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
				[]string{"ap list", "ap status " + sessionName + " --json"},
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_READ_FAILED",
			fmt.Sprintf("failed to read state for session %q", sessionName),
			err.Error(),
			"ap resume <session> [--project-root DIR] [--json]",
			nil,
		))
	}

	// Determine action based on current status.
	switch row.Status {
	case "running":
		return resumeAlreadyRunningStore(deps, sessionName, row)
	case "paused", "failed":
		return resumeSessionStore(deps, selectedStore, ctx, sessionName, row, contextOverride)
	case "completed":
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"SESSION_COMPLETED",
			fmt.Sprintf("session %q is already completed", sessionName),
			"Use 'ap run' to start a new session.",
			"ap resume <session> [--json]",
			[]string{"ap run <spec> <new-session>"},
		))
	case "aborted":
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"SESSION_ABORTED",
			fmt.Sprintf("session %q was aborted and cannot be resumed", sessionName),
			"Use 'ap run' to start a new session.",
			"ap resume <session> [--json]",
			[]string{"ap run <spec> <new-session>"},
		))
	default:
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"INVALID_STATE",
			fmt.Sprintf("session %q has unexpected status %q", sessionName, row.Status),
			"",
			"ap resume <session> [--json]",
			[]string{"ap status " + sessionName + " --json"},
		))
	}
}

func resumeAlreadyRunningStore(deps cliDeps, session string, row *store.SessionRow) int {
	resumeFrom := row.IterationCompleted + 1
	payload := map[string]any{
		"session":     session,
		"action":      "already_running",
		"status":      row.Status,
		"iteration":   row.Iteration,
		"resume_from": resumeFrom,
	}
	return renderResumeSuccess(deps, payload)
}

func resumeSessionStore(deps cliDeps, sessionStore *store.Store, ctx context.Context, session string, row *store.SessionRow, contextOverride string) int {
	// Clean up any iterations orphaned by a crash before resuming.
	orphaned, err := sessionStore.CleanOrphanedIterations(ctx, session)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"ORPHAN_CLEANUP_ERROR",
			fmt.Sprintf("failed to clean orphaned iterations for session %q", session),
			err.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}

	resumeFrom := row.IterationCompleted + 1

	err = sessionStore.UpdateSession(ctx, session, map[string]any{
		"status": "running",
	})
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_TRANSITION_ERROR",
			fmt.Sprintf("failed to resume session %q", session),
			err.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}

	payload := map[string]any{
		"session":     session,
		"action":      "resumed",
		"status":      "running",
		"resume_from": resumeFrom,
	}
	if orphaned > 0 {
		payload["orphaned_cleaned"] = orphaned
	}
	if contextOverride != "" {
		payload["context_override"] = contextOverride
	}

	// Re-launch the session process via launcher.
	var req RunRequestFile
	if row.RunRequestJSON != "" && row.RunRequestJSON != "{}" {
		if parseErr := json.Unmarshal([]byte(row.RunRequestJSON), &req); parseErr != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"RUN_REQUEST_PARSE_ERROR",
				fmt.Sprintf("failed to parse run request for session %q", session),
				parseErr.Error(),
				"ap resume <session> [--context TEXT] [--json]",
				nil,
			))
		}
	} else {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"MISSING_RUN_REQUEST",
			fmt.Sprintf("session %q has no stored run request", session),
			"The session cannot be resumed without a run request.",
			"ap resume <session> [--context TEXT] [--json]",
			[]string{"ap run <spec> <new-session>"},
		))
	}

	// Determine the run directory and write run_request.json to disk.
	runDir := req.RunDir
	if runDir == "" {
		projectRoot := strings.TrimSpace(sessionStore.ProjectRoot())
		if projectRoot == "" {
			projectRoot = strings.TrimSpace(req.ProjectRoot)
		}
		if projectRoot == "" {
			projectRoot = "."
		}
		runDir = filepath.Join(projectRoot, ".ap", "runs", session)
	}
	requestPath := filepath.Join(runDir, "run_request.json")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"RUN_DIR_ERROR",
			fmt.Sprintf("failed to create run directory for session %q", session),
			err.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}
	if err := WriteRunRequest(requestPath, req); err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"RUN_REQUEST_WRITE_ERROR",
			fmt.Sprintf("failed to write run request for session %q", session),
			err.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}

	// Build the runner command: ap _run --session NAME --request PATH --resume
	executable, execErr := os.Executable()
	if execErr != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EXECUTABLE_RESOLVE_ERROR",
			"failed to resolve ap executable path",
			execErr.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}
	runnerCmd := []string{executable, "_run", "--session", session, "--request", requestPath, "--resume"}

	// Resolve launcher.
	launcher := deps.launcher
	if launcher == nil {
		launcher = sessionpkg.NewTmuxLauncher()
	}
	if !launcher.Available() {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"LAUNCHER_UNAVAILABLE",
			fmt.Sprintf("launcher %q is not available to re-launch session %q", launcher.Name(), session),
			"Ensure tmux is installed and running.",
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}

	// Kill any existing tmux session for this name before re-launching.
	_ = launcher.Kill(session)

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(req.ProjectRoot)
	}
	handle, launchErr := launcher.Start(session, runnerCmd, sessionpkg.StartOptions{
		WorkDir: workDir,
		Env:     req.Env,
	})
	if launchErr != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"LAUNCH_FAILED",
			fmt.Sprintf("failed to re-launch session %q", session),
			launchErr.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			[]string{"ap status " + session + " --json", "ap kill " + session},
		))
	}

	payload["launched"] = true
	payload["launcher"] = launcher.Name()
	payload["pid"] = handle.PID

	return renderResumeSuccess(deps, payload)
}

func renderResumeSuccess(deps cliDeps, payload map[string]any) int {
	if deps.mode == output.ModeJSON {
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render resume output",
				err.Error(),
				"ap resume <session> [--json]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	action := payload["action"].(string)
	session := payload["session"].(string)
	switch action {
	case "already_running":
		_, _ = fmt.Fprintf(deps.stdout, "Session %q is already running.\n", session)
	case "resumed":
		_, _ = fmt.Fprintf(deps.stdout, "Resuming session %q from iteration %v.\n", session, payload["resume_from"])
	default:
		_, _ = fmt.Fprintf(deps.stdout, "Session %q: %s\n", session, action)
	}
	return output.ExitSuccess
}

func parseResumeArgs(args []string) (string, string, string, *output.ErrorResponse) {
	var sessionName, contextOverride, projectRoot string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue
		case arg == "--project-root" || strings.HasPrefix(arg, "--project-root=") || arg == "--workdir" || strings.HasPrefix(arg, "--workdir="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					err.Error(),
					"",
					"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
					[]string{"ap resume my-session --project-root /abs/path --json"},
				)
				return "", "", "", &errResp
			}
			i = next
			projectRoot = strings.TrimSpace(value)
		case arg == "--context" || arg == "-c" || strings.HasPrefix(arg, "--context=") || strings.HasPrefix(arg, "-c="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					err.Error(),
					"",
					"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
					[]string{"ap resume my-session --context \"focus on tests\""},
				)
				return "", "", "", &errResp
			}
			i = next
			contextOverride = value
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap resume accepts --project-root/--workdir, --context/-c, and --json.",
				"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
				[]string{"ap resume my-session", "ap resume my-session --project-root /abs/path --context \"new focus\" --json"},
			)
			return "", "", "", &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap resume takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
					[]string{"ap resume my-session"},
				)
				return "", "", "", &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	if sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			"Provide the session name to resume.",
			"ap resume <session> [--project-root DIR] [--context TEXT] [--json]",
			[]string{"ap resume my-session", "ap resume my-session --json"},
		)
		return "", "", "", &errResp
	}

	return sessionName, contextOverride, projectRoot, nil
}
