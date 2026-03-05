package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/lock"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/runner"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/store"
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

	locksDir := filepath.Join(projectRoot, ".ap", "locks")

	wasRunning := false
	var childrenKilled []string

	// Resolve launcher: use injected launcher or default to tmux.
	launcher := deps.launcher
	if launcher == nil {
		launcher = session.NewTmuxLauncher()
	}

	// Try to kill via launcher (primary backend). Kill is idempotent,
	// so success does not imply the session was running.
	if launcher.Available() {
		_ = launcher.Kill(sessionName)
	}

	ctx := context.Background()

	// Update session state via store.
	if deps.store != nil {
		row, storeErr := deps.store.GetSession(ctx, sessionName)
		if storeErr == nil {
			switch row.Status {
			case "completed", "failed", "aborted":
				// Already terminal — nothing to do.
			default:
				wasRunning = true
				_ = deps.store.UpdateSession(ctx, sessionName, map[string]any{
					"status": "aborted",
				})
			}
			// Cascade kill to child sessions.
			children, _ := deps.store.GetChildren(ctx, sessionName)
			for _, child := range children {
				if killChildSessionStore(ctx, deps.store, locksDir, child, launcher) {
					childrenKilled = append(childrenKilled, child)
				}
			}
		} else if !errors.Is(storeErr, store.ErrNotFound) {
			_, _ = fmt.Fprintf(deps.stderr, "warning: store error: %v\n", storeErr)
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

// killChildSessionStore attempts to abort a child session via the store.
// Returns true if the child was running and was successfully aborted.
func killChildSessionStore(ctx context.Context, s *store.Store, locksDir, child string, launcher session.Launcher) bool {
	row, err := s.GetSession(ctx, child)
	if err != nil {
		return false
	}
	switch row.Status {
	case "completed", "failed", "aborted":
		return false // already terminal
	}
	if launcher != nil && launcher.Available() {
		_ = launcher.Kill(child)
	}
	_ = s.UpdateSession(ctx, child, map[string]any{
		"status": "aborted",
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
