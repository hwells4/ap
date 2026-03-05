package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/controlplane"
)

// ErrNotFound is returned when a session is not found.
var ErrNotFound = errors.New("store: not found")

// SessionRow represents a row in the sessions table.
type SessionRow struct {
	Name               string
	Type               string
	Pipeline           string
	Status             string
	NodeID             string
	Iteration          int
	IterationCompleted int
	StartedAt          string
	CompletedAt        *string
	CurrentStage       string
	StagesJSON         string
	HistoryJSON        string
	Error              *string
	ErrorType          *string
	EscalationJSON     *string
	ParentSession      string
	ChildSessionsJSON  string
	RunRequestJSON     string
	ProjectRoot        string
	RepoRoot           string
	ConfigRoot         string
	ProjectKey         string
	TargetSource       string
	CreatedAt          string
	UpdatedAt          string
}

// CreateSession inserts a new session row.
func (s *Store) CreateSession(ctx context.Context, name, sessionType, pipeline, runRequestJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (name, type, pipeline, status, started_at, stages_json, history_json, child_sessions_json, run_request_json, created_at, updated_at)
		VALUES (?, ?, ?, 'running', ?, '[]', '[]', '[]', ?, ?, ?)`,
		name, sessionType, pipeline, now, runRequestJSON, now, now,
	)
	if err != nil {
		return fmt.Errorf("store: create session: %w", err)
	}
	s.syncSessionIndex(ctx, name)
	return nil
}

// GetSession returns a session by name, or nil and ErrNotFound.
func (s *Store) GetSession(ctx context.Context, name string) (*SessionRow, error) {
	row := s.db.QueryRowContext(ctx, `
			SELECT name, type, pipeline, status, node_id, iteration, iteration_completed,
			       started_at, completed_at, current_stage, stages_json, history_json,
			       error, error_type, escalation_json, parent_session, child_sessions_json,
			       run_request_json, project_root, repo_root, config_root, project_key, target_source,
			       created_at, updated_at
			FROM sessions WHERE name = ?`, name)

	var r SessionRow
	err := row.Scan(
		&r.Name, &r.Type, &r.Pipeline, &r.Status, &r.NodeID,
		&r.Iteration, &r.IterationCompleted, &r.StartedAt, &r.CompletedAt,
		&r.CurrentStage, &r.StagesJSON, &r.HistoryJSON,
		&r.Error, &r.ErrorType, &r.EscalationJSON,
		&r.ParentSession, &r.ChildSessionsJSON, &r.RunRequestJSON,
		&r.ProjectRoot, &r.RepoRoot, &r.ConfigRoot, &r.ProjectKey, &r.TargetSource,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get session: %w", err)
	}
	return &r, nil
}

// ListSessions returns all sessions, optionally filtered by status.
// Pass empty statusFilter for all sessions.
func (s *Store) ListSessions(ctx context.Context, statusFilter string) ([]SessionRow, error) {
	var query string
	var args []any
	if statusFilter != "" {
		query = `SELECT name, type, pipeline, status, node_id, iteration, iteration_completed,
			                started_at, completed_at, current_stage, stages_json, history_json,
			                error, error_type, escalation_json, parent_session, child_sessions_json,
			                run_request_json, project_root, repo_root, config_root, project_key, target_source,
			                created_at, updated_at
			         FROM sessions WHERE status = ? ORDER BY name`
		args = append(args, statusFilter)
	} else {
		query = `SELECT name, type, pipeline, status, node_id, iteration, iteration_completed,
			                started_at, completed_at, current_stage, stages_json, history_json,
			                error, error_type, escalation_json, parent_session, child_sessions_json,
			                run_request_json, project_root, repo_root, config_root, project_key, target_source,
			                created_at, updated_at
			         FROM sessions ORDER BY name`
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()

	var result []SessionRow
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(
			&r.Name, &r.Type, &r.Pipeline, &r.Status, &r.NodeID,
			&r.Iteration, &r.IterationCompleted, &r.StartedAt, &r.CompletedAt,
			&r.CurrentStage, &r.StagesJSON, &r.HistoryJSON,
			&r.Error, &r.ErrorType, &r.EscalationJSON,
			&r.ParentSession, &r.ChildSessionsJSON, &r.RunRequestJSON,
			&r.ProjectRoot, &r.RepoRoot, &r.ConfigRoot, &r.ProjectKey, &r.TargetSource,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: list sessions scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// validUpdateKeys lists all columns that may be passed to UpdateSession.
var validUpdateKeys = map[string]bool{
	"status":              true,
	"iteration":           true,
	"iteration_completed": true,
	"completed_at":        true,
	"current_stage":       true,
	"node_id":             true,
	"error":               true,
	"error_type":          true,
	"escalation_json":     true,
	"stages_json":         true,
	"history_json":        true,
	"child_sessions_json": true,
	"project_root":        true,
	"repo_root":           true,
	"config_root":         true,
	"project_key":         true,
	"target_source":       true,
}

// UpdateSession dynamically updates the given fields on a session.
func (s *Store) UpdateSession(ctx context.Context, name string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for k, v := range updates {
		if !validUpdateKeys[k] {
			return fmt.Errorf("store: invalid update key %q", k)
		}
		setClauses = append(setClauses, k+" = ?")
		args = append(args, v)
	}
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, name)

	query := fmt.Sprintf("UPDATE sessions SET %s WHERE name = ?", strings.Join(setClauses, ", "))
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: update session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	s.syncSessionIndex(ctx, name)
	return nil
}

// DeleteSession removes a session and cascades to iterations, events, outputs.
func (s *Store) DeleteSession(ctx context.Context, name string) error {
	rec := s.readSessionIndexRecord(ctx, name)
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	if rec != nil && s.control != nil {
		_ = s.control.DeleteSession(rec.ProjectKey, rec.SessionName)
	}
	return nil
}

func (s *Store) syncSessionIndex(ctx context.Context, name string) {
	if s == nil || s.control == nil {
		return
	}
	rec := s.readSessionIndexRecord(ctx, name)
	if rec == nil {
		return
	}
	_ = s.control.UpsertProject(rec.ProjectRoot, rec.ProjectKey, s.path)
	_ = s.control.UpsertSession(*rec)
}

func (s *Store) syncAllSessionIndex(ctx context.Context) {
	if s == nil || s.control == nil {
		return
	}
	rows, err := s.ListSessions(ctx, "")
	if err != nil {
		return
	}
	for i := range rows {
		rec := s.sessionRecordFromRow(&rows[i])
		if rec == nil {
			continue
		}
		_ = s.control.UpsertProject(rec.ProjectRoot, rec.ProjectKey, s.path)
		_ = s.control.UpsertSession(*rec)
	}
}

func (s *Store) readSessionIndexRecord(ctx context.Context, name string) *controlplane.SessionRecord {
	row, err := s.GetSession(ctx, name)
	if err != nil {
		return nil
	}
	return s.sessionRecordFromRow(row)
}

func (s *Store) sessionRecordFromRow(row *SessionRow) *controlplane.SessionRecord {
	if row == nil {
		return nil
	}
	projectRoot := strings.TrimSpace(row.ProjectRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(s.projectRoot)
	}
	projectKey := strings.TrimSpace(row.ProjectKey)
	if projectKey == "" {
		projectKey = strings.TrimSpace(s.projectKey)
	}
	rec := &controlplane.SessionRecord{
		ProjectKey:         projectKey,
		ProjectRoot:        projectRoot,
		SessionName:        row.Name,
		Status:             row.Status,
		Iteration:          row.Iteration,
		IterationCompleted: row.IterationCompleted,
		StartedAt:          row.StartedAt,
		CompletedAt:        row.CompletedAt,
		CurrentStage:       row.CurrentStage,
		RepoRoot:           row.RepoRoot,
		ConfigRoot:         row.ConfigRoot,
		TargetSource:       row.TargetSource,
		UpdatedAt:          row.UpdatedAt,
	}
	return rec
}
