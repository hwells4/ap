package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/lock"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/runner"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/state"
)

func runKill(args []string, deps cliDeps) int {
	sessionName, errResp := parseKillArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	projectRoot := "."
	if deps.getwd != nil {
		if cwd, err := deps.getwd(); err == nil {
			projectRoot = cwd
		}
	}

	runsDir := filepath.Join(projectRoot, ".ap", "runs")
	locksDir := filepath.Join(projectRoot, ".ap", "locks")
	sessionDir := filepath.Join(runsDir, sessionName)
	statePath := filepath.Join(sessionDir, "state.json")

	wasRunning := false
	var childrenKilled []string

	// Try to kill via TmuxLauncher (primary backend). Kill is idempotent,
	// so success does not imply the session was running.
	tmux := session.NewTmuxLauncher()
	if tmux.Available() {
		_ = tmux.Kill(sessionName)
	}

	// Update state.json if it exists.
	if _, err := os.Stat(statePath); err == nil {
		st, loadErr := state.Load(statePath)
		if loadErr == nil {
			switch st.Status {
			case state.StateCompleted, state.StateFailed, state.StateAborted:
				// Already terminal — nothing to do.
			default:
				wasRunning = true
				_, _ = state.Update(statePath, func(s *state.SessionState) error {
					s.Status = state.StateAborted
					return nil
				})
			}

			// Cascade kill to child sessions.
			for _, child := range st.ChildSessions {
				if killChildSession(runsDir, locksDir, child, tmux) {
					childrenKilled = append(childrenKilled, child)
				}
			}
		}
	}

	// Release the session lock if it exists.
	lk, err := lock.Acquire(locksDir, sessionName)
	if err == nil {
		// We acquired it (was either stale or unlocked) — release immediately.
		_ = lk.Release()
	}
	// If lock is held by a live process, the kill above should have terminated it.
	// Give it a moment and try once more if the initial acquire failed.
	if err != nil && strings.Contains(err.Error(), "locked") {
		lk2, err2 := lock.Acquire(locksDir, sessionName)
		if err2 == nil {
			_ = lk2.Release()
		}
	}

	// Release in_progress beads labeled pipeline/{session} (best-effort).
	_ = runner.ReleaseBeads(sessionName, "")

	// Build response.
	status := "killed"
	if !wasRunning {
		status = "not_running"
	}

	payload := map[string]any{
		"session":         sessionName,
		"status":          status,
		"was_running":     wasRunning,
		"children_killed": childrenKilled,
	}

	if deps.mode == output.ModeJSON {
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render kill output",
				err.Error(),
				"ap kill <session> [--json]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if wasRunning {
		_, _ = fmt.Fprintf(deps.stdout, "Killed session %q.\n", sessionName)
	} else {
		_, _ = fmt.Fprintf(deps.stdout, "Session %q was not running.\n", sessionName)
	}
	return output.ExitSuccess
}

// killChildSession attempts to abort a child session. Returns true if the
// child was running and was successfully aborted.
func killChildSession(runsDir, locksDir, child string, tmux *session.TmuxLauncher) bool {
	childStatePath := filepath.Join(runsDir, child, "state.json")
	if _, err := os.Stat(childStatePath); err != nil {
		return false
	}
	st, err := state.Load(childStatePath)
	if err != nil {
		return false
	}
	switch st.Status {
	case state.StateCompleted, state.StateFailed, state.StateAborted:
		return false // already terminal
	}
	if tmux != nil && tmux.Available() {
		_ = tmux.Kill(child)
	}
	_, _ = state.Update(childStatePath, func(s *state.SessionState) error {
		s.Status = state.StateAborted
		return nil
	})
	lk, err := lock.Acquire(locksDir, child)
	if err == nil {
		_ = lk.Release()
	}
	return true
}

func parseKillArgs(args []string) (string, *output.ErrorResponse) {
	// Safety: NO fuzzy matching on kill — exact arguments only.
	var sessionName string

	for _, arg := range args {
		switch {
		case arg == "--json":
			continue // handled globally
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap kill accepts no flags other than --json.",
				"ap kill <session> [--json]",
				[]string{"ap kill my-session", "ap kill my-session --json"},
			)
			return "", &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap kill takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap kill <session> [--json]",
					[]string{"ap kill my-session"},
				)
				return "", &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	if sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			"Provide the session name to kill.",
			"ap kill <session> [--json]",
			[]string{"ap kill my-session", "ap kill my-session --json"},
		)
		return "", &errResp
	}

	return sessionName, nil
}
