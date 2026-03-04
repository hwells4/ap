package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/output"
)

// watchHook maps an event type pattern to a shell command.
type watchHook struct {
	EventType string
	Command   string
}

func runWatch(args []string, deps cliDeps) int {
	sessionName, hooks, errResp := parseWatchArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	projectRoot := "."
	if deps.getwd != nil {
		if cwd, err := deps.getwd(); err == nil {
			projectRoot = cwd
		}
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

	eventsPath := filepath.Join(projectRoot, ".ap", "runs", sessionName, "events.jsonl")
	sessionDir := filepath.Join(projectRoot, ".ap", "runs", sessionName)
	if _, err := os.Stat(sessionDir); err != nil {
		return renderError(deps, output.ExitNotFound, output.NewError(
			"SESSION_NOT_FOUND",
			fmt.Sprintf("session %q not found", sessionName),
			fmt.Sprintf("No session directory at %s", sessionDir),
			"ap watch <session> --on <event> <cmd> [--json]",
			[]string{"ap list"},
		))
	}

	return watchEvents(eventsPath, sessionName, hooks, deps)
}

func watchEvents(eventsPath, session string, hooks []watchHook, deps cliDeps) int {
	// Wait for events file to appear.
	for {
		if _, err := os.Stat(eventsPath); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	file, err := os.Open(eventsPath)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EVENTS_READ_FAILED",
			"failed to open events file",
			err.Error(),
			"ap watch <session> --on <event> <cmd>",
			nil,
		))
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			processWatchLine(line, session, hooks, deps)
			// Check for session-ending events.
			if isSessionEnd(line) {
				return output.ExitSuccess
			}
		}
		if err != nil {
			if err == io.EOF {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			_, _ = fmt.Fprintf(deps.stderr, "error watching events: %v\n", err)
			return output.ExitGeneralError
		}
	}
}

func processWatchLine(line, session string, hooks []watchHook, deps cliDeps) {
	var evt map[string]any
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return
	}

	eventType, _ := evt["type"].(string)
	for _, hook := range hooks {
		if matchEventType(eventType, hook.EventType) {
			cmd := expandWatchVars(hook.Command, session, evt)
			if deps.mode == output.ModeJSON {
				payload := map[string]any{
					"event":   eventType,
					"hook":    hook.EventType,
					"command": cmd,
				}
				data, _ := json.Marshal(payload)
				_, _ = fmt.Fprintln(deps.stdout, string(data))
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

func isSessionEnd(line string) bool {
	var evt struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return false
	}
	switch evt.Type {
	case "session.completed", "session.failed", "session.aborted":
		return true
	}
	return false
}

func parseWatchArgs(args []string) (string, []watchHook, *output.ErrorResponse) {
	var sessionName string
	var hooks []watchHook

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
					"ap watch <session> --on <event> <cmd> [--json]",
					[]string{`ap watch my-session --on completed "echo done"`},
				)
				return "", nil, &errResp
			}
			hooks = append(hooks, watchHook{
				EventType: args[i+1],
				Command:   args[i+2],
			})
			i += 3
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap watch accepts --on <event> <cmd> and --json.",
				"ap watch <session> --on <event> <cmd> [--json]",
				[]string{`ap watch my-session --on completed "echo done"`},
			)
			return "", nil, &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap watch takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap watch <session> --on <event> <cmd> [--json]",
					[]string{`ap watch my-session --on completed "echo done"`},
				)
				return "", nil, &errResp
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
			"ap watch <session> --on <event> <cmd> [--json]",
			[]string{`ap watch my-session --on completed "echo done"`},
		)
		return "", nil, &errResp
	}

	return sessionName, hooks, nil
}
