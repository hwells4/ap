package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// runQuery dispatches `ap query` subcommands.
// Usage:
//
//	ap query sessions [--status STATUS] [--json]
//	ap query iterations --session NAME [--stage NAME] [--json]
//	ap query events --session NAME [--type TYPE] [--after SEQ] [--json]
func runQuery(args []string, deps cliDeps) int {
	if len(args) == 0 {
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"INVALID_ARGUMENT",
			"missing query subcommand",
			"Provide a subcommand: sessions, iterations, or events.",
			"ap query <subcommand> [flags] [--json]",
			[]string{
				"ap query sessions --json",
				"ap query iterations --session my-session --json",
				"ap query events --session my-session --json",
			},
		))
	}

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	subArgs := args[1:]

	switch sub {
	case "sessions":
		return querySessionsCmd(subArgs, deps)
	case "iterations":
		return queryIterationsCmd(subArgs, deps)
	case "events":
		return queryEventsCmd(subArgs, deps)
	default:
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"UNKNOWN_SUBCOMMAND",
			fmt.Sprintf("unknown query subcommand %q", sub),
			"Supported subcommands: sessions, iterations, events.",
			"ap query <subcommand> [flags] [--json]",
			[]string{
				"ap query sessions --json",
				"ap query iterations --session my-session --json",
				"ap query events --session my-session --json",
			},
		))
	}
}

func querySessionsCmd(args []string, deps cliDeps) int {
	var statusFilter string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue
		case arg == "--status" || strings.HasPrefix(arg, "--status="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"INVALID_ARGUMENT", err.Error(), "",
					"ap query sessions [--status STATUS] [--json]", nil,
				))
			}
			i = next
			statusFilter = strings.TrimSpace(value)
		case strings.HasPrefix(arg, "-"):
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap query sessions accepts --status and --json.",
				"ap query sessions [--status STATUS] [--json]",
				[]string{"ap query sessions --json", "ap query sessions --status running --json"},
			))
		default:
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unexpected argument %q", arg),
				"",
				"ap query sessions [--status STATUS] [--json]",
				nil,
			))
		}
	}

	ctx := context.Background()
	rows, err := deps.store.ListSessions(ctx, statusFilter)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"QUERY_FAILED", "failed to query sessions", err.Error(),
			"ap query sessions [--json]", nil,
		))
	}

	sessions := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		sessions = append(sessions, sessionRowToSummary(&r))
	}

	if deps.mode == output.ModeJSON {
		payload := output.NewSuccess(map[string]any{"sessions": sessions, "count": len(sessions)}, deps.corrections)
		serialized, _ := output.MarshalSuccess(payload)
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(deps.stdout, "No sessions found.")
		return output.ExitSuccess
	}
	for _, s := range sessions {
		_, _ = fmt.Fprintf(deps.stdout, "%-20s  %-10s  iter=%d/%d\n",
			s["name"], s["status"], s["iteration_completed"], s["iteration"])
	}
	return output.ExitSuccess
}

func queryIterationsCmd(args []string, deps cliDeps) int {
	var sessionName, stageFilter string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue
		case arg == "--session" || strings.HasPrefix(arg, "--session="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"INVALID_ARGUMENT", err.Error(), "",
					"ap query iterations --session NAME [--stage NAME] [--json]", nil,
				))
			}
			i = next
			sessionName = strings.TrimSpace(value)
		case arg == "--stage" || strings.HasPrefix(arg, "--stage="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"INVALID_ARGUMENT", err.Error(), "",
					"ap query iterations --session NAME [--stage NAME] [--json]", nil,
				))
			}
			i = next
			stageFilter = strings.TrimSpace(value)
		case strings.HasPrefix(arg, "-"):
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"",
				"ap query iterations --session NAME [--stage NAME] [--json]",
				[]string{"ap query iterations --session my-session --json"},
			))
		default:
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unexpected argument %q", arg),
				"",
				"ap query iterations --session NAME [--stage NAME] [--json]",
				nil,
			))
		}
	}

	if sessionName == "" {
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"INVALID_ARGUMENT",
			"missing required flag --session",
			"",
			"ap query iterations --session NAME [--stage NAME] [--json]",
			[]string{"ap query iterations --session my-session --json"},
		))
	}

	ctx := context.Background()
	rows, err := deps.store.GetIterations(ctx, sessionName, stageFilter)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"QUERY_FAILED", "failed to query iterations", err.Error(),
			"ap query iterations --session NAME [--json]", nil,
		))
	}

	iterations := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		iterations = append(iterations, iterationRowToMap(r))
	}

	if deps.mode == output.ModeJSON {
		payload := output.NewSuccess(map[string]any{
			"session":    sessionName,
			"iterations": iterations,
			"count":      len(iterations),
		}, deps.corrections)
		serialized, _ := output.MarshalSuccess(payload)
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if len(iterations) == 0 {
		_, _ = fmt.Fprintln(deps.stdout, "No iterations found.")
		return output.ExitSuccess
	}
	for _, it := range iterations {
		_, _ = fmt.Fprintf(deps.stdout, "iter=%-3v  stage=%-15v  status=%-10v  decision=%v\n",
			it["iteration"], it["stage_name"], it["status"], it["decision"])
	}
	return output.ExitSuccess
}

func queryEventsCmd(args []string, deps cliDeps) int {
	var sessionName, typeFilter string
	var afterSeq int
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue
		case arg == "--session" || strings.HasPrefix(arg, "--session="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"INVALID_ARGUMENT", err.Error(), "",
					"ap query events --session NAME [--type TYPE] [--after SEQ] [--json]", nil,
				))
			}
			i = next
			sessionName = strings.TrimSpace(value)
		case arg == "--type" || strings.HasPrefix(arg, "--type="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"INVALID_ARGUMENT", err.Error(), "",
					"ap query events --session NAME [--type TYPE] [--after SEQ] [--json]", nil,
				))
			}
			i = next
			typeFilter = strings.TrimSpace(value)
		case arg == "--after" || strings.HasPrefix(arg, "--after="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"INVALID_ARGUMENT", err.Error(), "",
					"ap query events --session NAME [--type TYPE] [--after SEQ] [--json]", nil,
				))
			}
			i = next
			n, convErr := parsePositiveIntOrZero(value)
			if convErr != nil {
				return renderError(deps, output.ExitInvalidArgs, output.NewError(
					"INVALID_ARGUMENT",
					fmt.Sprintf("--after requires a non-negative integer, got %q", value),
					"",
					"ap query events --session NAME [--after SEQ] [--json]",
					nil,
				))
			}
			afterSeq = n
		case strings.HasPrefix(arg, "-"):
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"",
				"ap query events --session NAME [--type TYPE] [--after SEQ] [--json]",
				[]string{"ap query events --session my-session --json"},
			))
		default:
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unexpected argument %q", arg),
				"",
				"ap query events --session NAME [--type TYPE] [--after SEQ] [--json]",
				nil,
			))
		}
	}

	if sessionName == "" {
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"INVALID_ARGUMENT",
			"missing required flag --session",
			"",
			"ap query events --session NAME [--type TYPE] [--after SEQ] [--json]",
			[]string{"ap query events --session my-session --json"},
		))
	}

	ctx := context.Background()
	rows, err := deps.store.GetEvents(ctx, sessionName, typeFilter, afterSeq)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"QUERY_FAILED", "failed to query events", err.Error(),
			"ap query events --session NAME [--json]", nil,
		))
	}

	events := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		events = append(events, eventRowToMap(r))
	}

	if deps.mode == output.ModeJSON {
		payload := output.NewSuccess(map[string]any{
			"session": sessionName,
			"events":  events,
			"count":   len(events),
		}, deps.corrections)
		serialized, _ := output.MarshalSuccess(payload)
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if len(events) == 0 {
		_, _ = fmt.Fprintln(deps.stdout, "No events found.")
		return output.ExitSuccess
	}
	for _, evt := range events {
		_, _ = fmt.Fprintf(deps.stdout, "seq=%-4v  type=%-25v  ts=%v\n",
			evt["seq"], evt["type"], evt["created_at"])
	}
	return output.ExitSuccess
}

func sessionRowToSummary(r *store.SessionRow) map[string]any {
	return map[string]any{
		"name":                r.Name,
		"type":                r.Type,
		"pipeline":            r.Pipeline,
		"status":              r.Status,
		"iteration":           r.Iteration,
		"iteration_completed": r.IterationCompleted,
		"current_stage":       r.CurrentStage,
		"started_at":          r.StartedAt,
		"project_root":        r.ProjectRoot,
		"repo_root":           r.RepoRoot,
		"config_root":         r.ConfigRoot,
		"project_key":         r.ProjectKey,
		"target_source":       r.TargetSource,
	}
}

func iterationRowToMap(r store.IterationRow) map[string]any {
	m := map[string]any{
		"id":           r.ID,
		"session_name": r.SessionName,
		"stage_name":   r.StageName,
		"iteration":    r.Iteration,
		"status":       r.Status,
		"decision":     r.Decision,
		"summary":      r.Summary,
		"exit_code":    r.ExitCode,
		"started_at":   r.StartedAt,
	}
	if r.CompletedAt != nil {
		m["completed_at"] = *r.CompletedAt
	}
	var signals any
	if json.Unmarshal([]byte(r.SignalsJSON), &signals) == nil {
		m["signals"] = signals
	}
	return m
}

func eventRowToMap(r store.EventRow) map[string]any {
	m := map[string]any{
		"id":           r.ID,
		"session_name": r.SessionName,
		"seq":          r.Seq,
		"type":         r.Type,
		"created_at":   r.CreatedAt,
	}
	var cursor any
	if json.Unmarshal([]byte(r.CursorJSON), &cursor) == nil {
		m["cursor"] = cursor
	}
	var data any
	if json.Unmarshal([]byte(r.DataJSON), &data) == nil {
		m["data"] = data
	}
	return m
}

func parsePositiveIntOrZero(s string) (int, error) {
	s = strings.TrimSpace(s)
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a non-negative integer: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
