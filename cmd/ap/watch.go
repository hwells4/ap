package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	osExec "os/exec"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// watchHook maps an event type pattern to a shell command.
type watchHook struct {
	EventType string
	Command   string
}

func runWatch(args []string, deps cliDeps) int {
	sessionName, hooks, projectRootFlag, errResp := parseWatchArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	// Merge config hooks if no CLI hooks provided.
	if len(hooks) == 0 {
		cfg, _ := config.Load("")
		cfgHooks := cfg.WatchHooks()
		if cfgHooks.OnCompleted != "" {
			hooks = append(hooks, watchHook{EventType: "session.completed", Command: cfgHooks.OnCompleted})
		}
		if cfgHooks.OnEscalate != "" {
			hooks = append(hooks, watchHook{EventType: "signal.escalate", Command: cfgHooks.OnEscalate})
		}
		if cfgHooks.OnIdle != "" {
			hooks = append(hooks, watchHook{EventType: "idle", Command: cfgHooks.OnIdle})
		}
	}

	if len(hooks) == 0 {
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"NO_HOOKS",
			"no watch hooks configured",
			"Provide --on flags or configure hooks in ~/.config/ap/config.yaml.",
			"ap watch <session> --on completed <cmd> [--json]",
			[]string{
				`ap watch my-session --on completed "notify-send done"`,
				`ap watch my-session --on escalate "echo escalated"`,
			},
		))
	}

	ctx := context.Background()

	selectedStore, cleanup, lookupErr := resolveSessionStore(ctx, deps, sessionName, projectRootFlag)
	if lookupErr != nil {
		if errors.Is(lookupErr, errSessionLookupNotFound) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				"No session with that name in local or machine-wide index.",
				"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
				[]string{"ap query sessions --status running --json"},
			))
		}
		var ambiguous *sessionLookupAmbiguousError
		if errors.As(lookupErr, &ambiguous) {
			suggestions := []string{}
			for _, match := range ambiguous.Matches {
				suggestions = append(suggestions, fmt.Sprintf("ap watch %s --project-root %s --on completed \"echo done\" --json", sessionName, match.ProjectRoot))
				if len(suggestions) >= 3 {
					break
				}
			}
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"SESSION_AMBIGUOUS",
				lookupErr.Error(),
				"Use --project-root to select the project explicitly.",
				"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
				suggestions,
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EVENTS_READ_FAILED",
			fmt.Sprintf("failed to resolve store for session %q", sessionName),
			lookupErr.Error(),
			"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
			nil,
		))
	}
	defer cleanup()

	// Verify session exists.
	_, err := selectedStore.GetSession(ctx, sessionName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				"No session with that name in the store.",
				"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
				[]string{"ap list"},
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EVENTS_READ_FAILED",
			"failed to check session",
			err.Error(),
			"ap watch <session> --on <event> <cmd> [--project-root DIR]",
			nil,
		))
	}

	return watchEventsStore(ctx, sessionName, hooks, selectedStore, deps)
}

func watchEventsStore(ctx context.Context, session string, hooks []watchHook, sessionStore *store.Store, deps cliDeps) int {
	lastSeq := 0
	for {
		events, err := sessionStore.TailEvents(ctx, session, lastSeq)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "error watching events: %v\n", err)
			return output.ExitGeneralError
		}
		for _, evt := range events {
			processWatchEvent(evt, session, hooks, deps)
			if evt.Seq > lastSeq {
				lastSeq = evt.Seq
			}
			// Check for session-ending events.
			if isSessionEndType(evt.Type) {
				return output.ExitSuccess
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func processWatchEvent(evt store.EventRow, session string, hooks []watchHook, deps cliDeps) {
	for _, hook := range hooks {
		if matchEventType(evt.Type, hook.EventType) {
			// Build event map for variable expansion.
			evtMap := map[string]any{"type": evt.Type}
			var cursor map[string]any
			if json.Unmarshal([]byte(evt.CursorJSON), &cursor) == nil {
				evtMap["cursor"] = cursor
			}
			var data map[string]any
			if json.Unmarshal([]byte(evt.DataJSON), &data) == nil {
				evtMap["data"] = data
			}

			cmd := expandWatchVars(hook.Command, session, evtMap)
			if deps.mode == output.ModeJSON {
				payload := map[string]any{
					"event":   evt.Type,
					"hook":    hook.EventType,
					"command": cmd,
				}
				out, _ := json.Marshal(payload)
				_, _ = fmt.Fprintln(deps.stdout, string(out))
			}
			execWatchCommand(cmd, deps)
		}
	}
}

func matchEventType(actual, pattern string) bool {
	actual = strings.ToLower(strings.TrimSpace(actual))
	pattern = strings.ToLower(strings.TrimSpace(pattern))

	// Direct match.
	if actual == pattern {
		return true
	}

	// Shorthand aliases.
	switch pattern {
	case "completed":
		return actual == "session.completed"
	case "escalate", "escalated":
		return actual == "signal.escalate"
	case "failed":
		return actual == "session.failed" || actual == "iteration.failed"
	case "idle":
		return actual == "session.idle"
	}

	return false
}

func expandWatchVars(cmd, session string, evt map[string]any) string {
	result := cmd
	result = strings.ReplaceAll(result, "${SESSION}", session)

	eventType, _ := evt["type"].(string)
	result = strings.ReplaceAll(result, "${EVENT}", eventType)

	if data, ok := evt["data"].(map[string]any); ok {
		if reason, ok := data["reason"].(string); ok {
			result = strings.ReplaceAll(result, "${REASON}", reason)
		}
	}

	if cursor, ok := evt["cursor"].(map[string]any); ok {
		if iter, ok := cursor["iteration"].(float64); ok {
			result = strings.ReplaceAll(result, "${ITERATION}", fmt.Sprintf("%d", int(iter)))
		}
	}

	return result
}

func execWatchCommand(cmd string, deps cliDeps) {
	c := osExec.Command("sh", "-c", cmd)
	c.Stdout = deps.stdout
	c.Stderr = deps.stderr
	_ = c.Run()
}

func isSessionEndType(eventType string) bool {
	switch eventType {
	case "session.completed", "session.failed", "session.aborted":
		return true
	}
	return false
}

func parseWatchArgs(args []string) (string, []watchHook, string, *output.ErrorResponse) {
	var sessionName string
	var hooks []watchHook
	var projectRoot string

	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--json":
			i++
			continue
		case arg == "--on":
			if i+2 >= len(args) {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"--on requires two arguments: <event> <command>",
					"Example: --on completed \"notify-send done\"",
					"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
					[]string{`ap watch my-session --on completed "echo done"`},
				)
				return "", nil, "", &errResp
			}
			hooks = append(hooks, watchHook{
				EventType: args[i+1],
				Command:   args[i+2],
			})
			i += 3
		case arg == "--project-root" || strings.HasPrefix(arg, "--project-root=") || arg == "--workdir" || strings.HasPrefix(arg, "--workdir="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					err.Error(),
					"",
					"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
					[]string{`ap watch my-session --project-root /abs/path --on completed "echo done"`},
				)
				return "", nil, "", &errResp
			}
			i = next + 1
			projectRoot = strings.TrimSpace(value)
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap watch accepts --on <event> <cmd>, --project-root/--workdir, and --json.",
				"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
				[]string{`ap watch my-session --on completed "echo done"`},
			)
			return "", nil, "", &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap watch takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
					[]string{`ap watch my-session --on completed "echo done"`},
				)
				return "", nil, "", &errResp
			}
			sessionName = strings.TrimSpace(arg)
			i++
		}
	}

	if sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			"Provide the session name to watch.",
			"ap watch <session> --on <event> <cmd> [--project-root DIR] [--json]",
			[]string{`ap watch my-session --on completed "echo done"`},
		)
		return "", nil, "", &errResp
	}

	return sessionName, hooks, projectRoot, nil
}
