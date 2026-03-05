package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

func runStatus(args []string, deps cliDeps) int {
	sessionName, errResp := parseStatusArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	ctx := context.Background()

	if deps.store == nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STORE_NOT_AVAILABLE",
			"session store is not available",
			"The session database could not be opened.",
			"ap status <session> [--json]",
			nil,
		))
	}

	row, err := deps.store.GetSession(ctx, sessionName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				"No session found in store.",
				"ap status <session> [--json]",
				[]string{"ap list", "ap status my-session --json"},
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_READ_FAILED",
			fmt.Sprintf("failed to read state for session %q", sessionName),
			err.Error(),
			"ap status <session> [--json]",
			nil,
		))
	}

	return renderStatusFromRow(deps, row)
}

func renderStatusFromRow(deps cliDeps, row *store.SessionRow) int {
	if deps.mode == output.ModeJSON {
		payload := output.NewSuccess(map[string]any{"snapshot": sessionRowToSnapshot(row)}, deps.corrections)
		serialized, err := output.MarshalSuccess(payload)
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render status output",
				err.Error(),
				"ap status <session> [--json]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}
	_, _ = fmt.Fprint(deps.stdout, renderStatusHuman(row))
	return output.ExitSuccess
}

// sessionRowToSnapshot converts a SessionRow to a map for JSON output.
func sessionRowToSnapshot(r *store.SessionRow) map[string]any {
	snapshot := map[string]any{
		"session":             r.Name,
		"type":                r.Type,
		"pipeline":            r.Pipeline,
		"status":              r.Status,
		"node_id":             r.NodeID,
		"iteration":           r.Iteration,
		"iteration_completed": r.IterationCompleted,
		"started_at":          r.StartedAt,
		"current_stage":       r.CurrentStage,
		"parent_session":      r.ParentSession,
		"project_root":        r.ProjectRoot,
		"repo_root":           r.RepoRoot,
		"config_root":         r.ConfigRoot,
		"project_key":         r.ProjectKey,
		"target_source":       r.TargetSource,
		"run_target": map[string]any{
			"project_root": r.ProjectRoot,
			"repo_root":    r.RepoRoot,
			"config_root":  r.ConfigRoot,
			"project_key":  r.ProjectKey,
			"source":       r.TargetSource,
		},
	}
	if r.CompletedAt != nil {
		snapshot["completed_at"] = *r.CompletedAt
	}
	if r.Error != nil {
		snapshot["error"] = *r.Error
	}
	if r.ErrorType != nil {
		snapshot["error_type"] = *r.ErrorType
	}
	if r.EscalationJSON != nil {
		var esc any
		if json.Unmarshal([]byte(*r.EscalationJSON), &esc) == nil {
			snapshot["escalation"] = esc
		}
	}
	// Decode JSON array fields.
	var stages any
	if json.Unmarshal([]byte(r.StagesJSON), &stages) == nil {
		snapshot["stages"] = stages
	}
	var history any
	if json.Unmarshal([]byte(r.HistoryJSON), &history) == nil {
		snapshot["history"] = history
	}
	var children any
	if json.Unmarshal([]byte(r.ChildSessionsJSON), &children) == nil {
		snapshot["child_sessions"] = children
	}
	return snapshot
}

func renderStatusHuman(r *store.SessionRow) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Session:    %s\n", r.Name))
	b.WriteString(fmt.Sprintf("Status:     %s\n", r.Status))
	if r.CurrentStage != "" {
		b.WriteString(fmt.Sprintf("Stage:      %s\n", r.CurrentStage))
	}
	if stagePos, stageTotal, ok := pipelineStageProgressFromRow(r); ok {
		b.WriteString(fmt.Sprintf("Pipeline:   stage %d of %d\n", stagePos, stageTotal))
	}
	if r.NodeID != "" {
		b.WriteString(fmt.Sprintf("Node:       %s\n", r.NodeID))
	}
	b.WriteString(fmt.Sprintf("Iteration:  %d (completed: %d)\n", r.Iteration, r.IterationCompleted))
	if r.ProjectRoot != "" {
		b.WriteString(fmt.Sprintf("Project:    %s\n", r.ProjectRoot))
	}
	if r.RepoRoot != "" {
		b.WriteString(fmt.Sprintf("Repo:       %s\n", r.RepoRoot))
	}
	if r.ConfigRoot != "" {
		b.WriteString(fmt.Sprintf("Config:     %s\n", r.ConfigRoot))
	}
	b.WriteString(fmt.Sprintf("Started:    %s\n", r.StartedAt))
	if r.CompletedAt != nil {
		b.WriteString(fmt.Sprintf("Completed:  %s\n", *r.CompletedAt))
	}
	if r.Error != nil {
		b.WriteString(fmt.Sprintf("Error:      %s\n", *r.Error))
	}
	if r.ParentSession != "" {
		b.WriteString(fmt.Sprintf("Parent:     %s\n", r.ParentSession))
	}
	var children []string
	_ = json.Unmarshal([]byte(r.ChildSessionsJSON), &children)
	if len(children) > 0 {
		b.WriteString(fmt.Sprintf("Children:   %s\n", strings.Join(children, ", ")))
	}
	return b.String()
}

func pipelineStageProgressFromRow(r *store.SessionRow) (int, int, bool) {
	if r == nil || r.CurrentStage == "" || r.StagesJSON == "" || r.StagesJSON == "[]" {
		return 0, 0, false
	}
	var stages []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(r.StagesJSON), &stages); err != nil {
		return 0, 0, false
	}
	for idx, s := range stages {
		if s.Name == r.CurrentStage {
			return idx + 1, len(stages), true
		}
	}
	return 0, 0, false
}

func parseStatusArgs(args []string) (string, *output.ErrorResponse) {
	var sessionName string

	for _, arg := range args {
		switch {
		case arg == "--json":
			continue
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap status accepts no flags other than --json.",
				"ap status <session> [--json]",
				[]string{"ap status my-session", "ap status my-session --json"},
			)
			return "", &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap status takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap status <session> [--json]",
					[]string{"ap status my-session"},
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
			"Provide the session name to check status.",
			"ap status <session> [--json]",
			[]string{"ap status my-session", "ap status my-session --json"},
		)
		return "", &errResp
	}

	return sessionName, nil
}
