package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/output"
)

func runLogs(args []string, deps cliDeps) int {
	sessionName, follow, errResp := parseLogsArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	projectRoot := "."
	if deps.getwd != nil {
		if cwd, err := deps.getwd(); err == nil {
			projectRoot = cwd
		}
	}

	eventsPath := filepath.Join(projectRoot, ".ap", "runs", sessionName, "events.jsonl")

	if _, err := os.Stat(eventsPath); err != nil {
		// Check if the session directory exists at all.
		sessionDir := filepath.Join(projectRoot, ".ap", "runs", sessionName)
		if _, dirErr := os.Stat(sessionDir); dirErr != nil {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				fmt.Sprintf("No session directory at %s", sessionDir),
				"ap logs <session> [-f] [--json]",
				[]string{"ap list", "ap logs my-session --json"},
			))
		}
		// Session exists but no events file yet — empty output.
		return output.ExitSuccess
	}

	if follow {
		return followLogs(eventsPath, deps)
	}
	return dumpLogs(eventsPath, deps)
}

func dumpLogs(eventsPath string, deps cliDeps) int {
	file, err := os.Open(eventsPath)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EVENTS_READ_FAILED",
			"failed to open events file",
			err.Error(),
			"ap logs <session> [--json]",
			nil,
		))
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if deps.mode == output.ModeJSON {
			_, _ = fmt.Fprintln(deps.stdout, line)
		} else {
			_, _ = fmt.Fprintln(deps.stdout, formatEventHuman(line))
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "error reading events: %v\n", err)
		return output.ExitGeneralError
	}
	return output.ExitSuccess
}

func followLogs(eventsPath string, deps cliDeps) int {
	file, err := os.Open(eventsPath)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EVENTS_READ_FAILED",
			"failed to open events file",
			err.Error(),
			"ap logs <session> -f [--json]",
			nil,
		))
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			if deps.mode == output.ModeJSON {
				_, _ = fmt.Fprintln(deps.stdout, line)
			} else {
				_, _ = fmt.Fprintln(deps.stdout, formatEventHuman(line))
			}
		}
		if err != nil {
			if err == io.EOF {
				// Poll for new data.
				time.Sleep(250 * time.Millisecond)
				continue
			}
			_, _ = fmt.Fprintf(deps.stderr, "error following events: %v\n", err)
			return output.ExitGeneralError
		}
	}
}

func formatEventHuman(line string) string {
	var evt map[string]any
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return line // fallback: print raw
	}

	ts, _ := evt["ts"].(string)
	eventType, _ := evt["type"].(string)
	session, _ := evt["session"].(string)

	// Compact timestamp to time only if today.
	if len(ts) > 10 {
		ts = ts[11:] // strip date prefix "YYYY-MM-DD"
		if idx := strings.Index(ts, "+"); idx > 0 {
			ts = ts[:idx]
		}
		if idx := strings.Index(ts, "Z"); idx > 0 {
			ts = ts[:idx]
		}
	}

	var b strings.Builder
	b.WriteString(ts)
	b.WriteString("  ")
	b.WriteString(eventType)
	if session != "" {
		b.WriteString("  [")
		b.WriteString(session)
		b.WriteString("]")
	}

	// Add cursor info if present.
	if cursor, ok := evt["cursor"].(map[string]any); ok {
		if iter, ok := cursor["iteration"].(float64); ok && iter > 0 {
			b.WriteString(fmt.Sprintf("  iter=%d", int(iter)))
		}
		if provider, ok := cursor["provider"].(string); ok && provider != "" {
			b.WriteString(fmt.Sprintf("  provider=%s", provider))
		}
	}

	// Add select data fields.
	if data, ok := evt["data"].(map[string]any); ok {
		for _, key := range []string{"decision", "reason", "error", "stage"} {
			if val, ok := data[key]; ok && val != nil && val != "" {
				b.WriteString(fmt.Sprintf("  %s=%v", key, val))
			}
		}
	}

	return b.String()
}

func parseLogsArgs(args []string) (string, bool, *output.ErrorResponse) {
	var sessionName string
	var follow bool

	for _, arg := range args {
		switch {
		case arg == "--json":
			continue
		case arg == "-f" || arg == "--follow":
			follow = true
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap logs accepts -f/--follow and --json.",
				"ap logs <session> [-f] [--json]",
				[]string{"ap logs my-session", "ap logs my-session -f --json"},
			)
			return "", false, &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap logs takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap logs <session> [-f] [--json]",
					[]string{"ap logs my-session"},
				)
				return "", false, &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	if sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			"Provide the session name to view logs.",
			"ap logs <session> [-f] [--json]",
			[]string{"ap logs my-session", "ap logs my-session -f"},
		)
		return "", false, &errResp
	}

	return sessionName, follow, nil
}
