package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/state"
)

type cleanResult struct {
	Session string `json:"session"`
	Bytes   int64  `json:"bytes_freed"`
}

type skipResult struct {
	Session string `json:"session"`
	Reason  string `json:"reason"`
}

func runClean(args []string, deps cliDeps) int {
	sessionName, all, force, errResp := parseCleanArgs(args)
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

	var cleaned []cleanResult
	var skipped []skipResult

	if all {
		entries, err := os.ReadDir(runsDir)
		if err != nil {
			if os.IsNotExist(err) {
				return renderCleanResult(deps, nil, nil)
			}
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to list sessions",
				err.Error(),
				"ap clean --all [--force] [--json]",
				nil,
			))
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			c, s := cleanSession(runsDir, locksDir, name, force)
			if c != nil {
				cleaned = append(cleaned, *c)
			}
			if s != nil {
				skipped = append(skipped, *s)
			}
		}
	} else {
		c, s := cleanSession(runsDir, locksDir, sessionName, force)
		if c != nil {
			cleaned = append(cleaned, *c)
		}
		if s != nil {
			skipped = append(skipped, *s)
		}
	}

	return renderCleanResult(deps, cleaned, skipped)
}

func cleanSession(runsDir, locksDir, session string, force bool) (*cleanResult, *skipResult) {
	sessionDir := filepath.Join(runsDir, session)
	statePath := filepath.Join(sessionDir, "state.json")

	// Session dir doesn't exist — nothing to do (idempotent).
	if _, err := os.Stat(sessionDir); err != nil {
		return nil, nil
	}

	// Check state to decide if cleaning is safe.
	snapshot, err := state.Load(statePath)
	if err == nil {
		switch snapshot.Status {
		case state.StateRunning, state.StatePending:
			if !force {
				return nil, &skipResult{Session: session, Reason: string(snapshot.Status)}
			}
		case state.StatePaused:
			if !force {
				return nil, &skipResult{Session: session, Reason: "paused"}
			}
		}
	}
	// If state.json is missing or corrupt, allow cleanup.

	// Calculate bytes.
	bytes := dirSize(sessionDir)

	// Remove session directory.
	_ = os.RemoveAll(sessionDir)

	// Remove lock file if exists.
	lockPath := filepath.Join(locksDir, session+".lock")
	_ = os.Remove(lockPath)

	return &cleanResult{Session: session, Bytes: bytes}, nil
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func renderCleanResult(deps cliDeps, cleaned []cleanResult, skipped []skipResult) int {
	if cleaned == nil {
		cleaned = []cleanResult{}
	}
	if skipped == nil {
		skipped = []skipResult{}
	}

	if deps.mode == output.ModeJSON {
		var totalBytes int64
		for _, c := range cleaned {
			totalBytes += c.Bytes
		}
		payload := map[string]any{
			"cleaned":     cleaned,
			"skipped":     skipped,
			"total_freed": totalBytes,
		}
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render clean output",
				err.Error(),
				"ap clean <session> [--json]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if len(cleaned) == 0 && len(skipped) == 0 {
		_, _ = fmt.Fprintln(deps.stdout, "Nothing to clean.")
		return output.ExitSuccess
	}
	for _, c := range cleaned {
		_, _ = fmt.Fprintf(deps.stdout, "Cleaned %q (%d bytes freed)\n", c.Session, c.Bytes)
	}
	for _, s := range skipped {
		_, _ = fmt.Fprintf(deps.stdout, "Skipped %q (%s)\n", s.Session, s.Reason)
	}
	return output.ExitSuccess
}

func parseCleanArgs(args []string) (string, bool, bool, *output.ErrorResponse) {
	// Safety: NO fuzzy matching on clean — exact arguments only.
	var sessionName string
	var all, force bool

	for _, arg := range args {
		switch {
		case arg == "--json":
			continue
		case arg == "--all":
			all = true
		case arg == "--force" || arg == "-f":
			force = true
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap clean accepts --all, --force/-f, and --json.",
				"ap clean <session> [--force] [--json] | ap clean --all [--force] [--json]",
				[]string{"ap clean my-session", "ap clean --all --json"},
			)
			return "", false, false, &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap clean takes exactly one session name or --all",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap clean <session> [--json]",
					[]string{"ap clean my-session", "ap clean --all"},
				)
				return "", false, false, &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	if all && sessionName != "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"--all and session name are mutually exclusive",
			fmt.Sprintf("Got --all and %q.", sessionName),
			"ap clean <session> | ap clean --all",
			[]string{"ap clean my-session", "ap clean --all"},
		)
		return "", false, false, &errResp
	}

	if !all && sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session> or --all",
			"Provide a session name or --all for bulk cleanup.",
			"ap clean <session> [--force] [--json] | ap clean --all [--force] [--json]",
			[]string{"ap clean my-session", "ap clean --all --json"},
		)
		return "", false, false, &errResp
	}

	return sessionName, all, force, nil
}
