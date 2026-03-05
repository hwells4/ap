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
	const killSyntax = "ap kill <session> [--project-root DIR] [--json]"
	parsed, errResp := parseKillArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}
	sessionName := parsed.SessionName
	projectRootFlag := parsed.ProjectRoot

	projectRoot := "."
	if deps.getwd != nil {
		if cwd, err := deps.getwd(); err == nil {
			projectRoot = cwd
		}
	}
	if strings.TrimSpace(projectRootFlag) != "" {
		resolved, rootErr := resolveRunProjectRoot(projectRootFlag, deps.getwd)
		if rootErr != nil {
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"INVALID_ARGUMENT",
				rootErr.Error(),
				"",
				killSyntax,
				[]string{"ap kill my-session --project-root /abs/path --json"},
			))
		}
		projectRoot = resolved
	}

	ctx := context.Background()
	selectedStore := deps.store
	cleanup := func() {}
	resolvedStore, resolvedCleanup, lookupErr := resolveSessionStore(ctx, deps, sessionName, projectRootFlag)
	if lookupErr == nil {
		selectedStore = resolvedStore
		cleanup = resolvedCleanup
		if resolvedRoot := strings.TrimSpace(selectedStore.ProjectRoot()); resolvedRoot != "" {
			projectRoot = resolvedRoot
		}
	} else {
		if !errors.Is(lookupErr, errSessionLookupNotFound) {
			var ambiguous *sessionLookupAmbiguousError
			if errors.As(lookupErr, &ambiguous) {
				suggestions := []string{}
				for _, match := range ambiguous.Matches {
					suggestions = append(suggestions, fmt.Sprintf("ap kill %s --project-root %s --json", sessionName, match.ProjectRoot))
					if len(suggestions) >= 3 {
						break
					}
				}
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"SESSION_AMBIGUOUS",
					lookupErr.Error(),
					"Use --project-root to select the project explicitly.",
					killSyntax,
					suggestions,
				))
			}
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"STATE_READ_FAILED",
				fmt.Sprintf("failed to resolve store for session %q", sessionName),
				lookupErr.Error(),
				killSyntax,
				nil,
			))
		}
	}
	defer cleanup()

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

	// Update session state via store.
	if selectedStore != nil {
		row, storeErr := selectedStore.GetSession(ctx, sessionName)
		if storeErr == nil {
			if root := strings.TrimSpace(row.ProjectRoot); root != "" {
				projectRoot = root
				locksDir = filepath.Join(projectRoot, ".ap", "locks")
			}
			switch row.Status {
			case "completed", "failed", "aborted":
				// Already terminal — nothing to do.
			default:
				wasRunning = true
				_ = selectedStore.UpdateSession(ctx, sessionName, map[string]any{
					"status": "aborted",
				})
			}
			// Cascade kill to child sessions.
			children, _ := selectedStore.GetChildren(ctx, sessionName)
			for _, child := range children {
				if killChildSessionStore(ctx, selectedStore, locksDir, child, launcher) {
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

func parseKillArgs(args []string) (parsedSessionArgs, *output.ErrorResponse) {
	// Safety: NO fuzzy matching on kill — exact arguments only.
	return parseSessionArgs(args, "kill", "ap kill <session> [--project-root DIR] [--json]", nil)
}
