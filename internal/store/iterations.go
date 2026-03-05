package store

import (
	"context"
	"fmt"
	"time"
)

// IterationInput holds the parameters for starting a new iteration.
type IterationInput struct {
	SessionName  string
	StageName    string
	Iteration    int
	ProviderName string
}

// IterationComplete holds the parameters for completing an iteration.
type IterationComplete struct {
	SessionName  string
	StageName    string
	Iteration    int
	Decision     string
	Summary      string
	ExitCode     int
	SignalsJSON  string
	Stdout       string
	Stderr       string
	ContextJSON  string
	ProviderName string
	DurationMS   int64
}

// IterationRow represents a row from the iterations table.
type IterationRow struct {
	ID          int64
	SessionName string
	StageName   string
	Iteration   int
	Status      string
	Decision    string
	Summary     string
	ExitCode    int
	SignalsJSON string
	StartedAt   string
	CompletedAt *string
}

// StartIteration inserts a new iteration, appends a started event, and
// updates the session — all within a single transaction.
func (s *Store) StartIteration(ctx context.Context, input IterationInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: start iteration begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO iterations (session_name, stage_name, iteration, status, started_at)
		VALUES (?, ?, ?, 'started', ?)`,
		input.SessionName, input.StageName, input.Iteration, now,
	)
	if err != nil {
		return fmt.Errorf("store: insert iteration: %w", err)
	}

	cursorJSON := fmt.Sprintf(`{"iteration":%d,"provider":"%s"}`, input.Iteration, input.ProviderName)
	dataJSON := fmt.Sprintf(`{"stage":"%s","iteration":%d}`, input.StageName, input.Iteration)
	if err := appendEventTx(ctx, tx, input.SessionName, "iteration.started", cursorJSON, dataJSON); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions SET iteration = ?, status = 'running', updated_at = ? WHERE name = ?`,
		input.Iteration, now, input.SessionName,
	)
	if err != nil {
		return fmt.Errorf("store: update session iteration: %w", err)
	}

	return tx.Commit()
}

// CompleteIteration marks an iteration done, inserts output, appends a
// completed event, and updates the session — all in one transaction.
func (s *Store) CompleteIteration(ctx context.Context, input IterationComplete) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: complete iteration begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	var iterID int64
	err = tx.QueryRowContext(ctx, `
		UPDATE iterations
		SET status = 'completed', decision = ?, summary = ?, exit_code = ?, signals_json = ?, completed_at = ?
		WHERE session_name = ? AND stage_name = ? AND iteration = ?
		RETURNING id`,
		input.Decision, input.Summary, input.ExitCode, input.SignalsJSON, now,
		input.SessionName, input.StageName, input.Iteration,
	).Scan(&iterID)
	if err != nil {
		return fmt.Errorf("store: update iteration: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO outputs (iteration_id, stdout, stderr, context_json)
		VALUES (?, ?, ?, ?)`,
		iterID, input.Stdout, input.Stderr, input.ContextJSON,
	)
	if err != nil {
		return fmt.Errorf("store: insert output: %w", err)
	}

	cursorJSON := fmt.Sprintf(`{"iteration":%d,"provider":"%s"}`, input.Iteration, input.ProviderName)
	dataJSON := fmt.Sprintf(`{"stage":"%s","iteration":%d,"decision":"%s","summary":"%s","duration":%d}`,
		input.StageName, input.Iteration, input.Decision, input.Summary, input.DurationMS)
	if err := appendEventTx(ctx, tx, input.SessionName, "iteration.completed", cursorJSON, dataJSON); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions SET iteration_completed = ?, updated_at = ? WHERE name = ?`,
		input.Iteration, now, input.SessionName,
	)
	if err != nil {
		return fmt.Errorf("store: update session iteration_completed: %w", err)
	}

	return tx.Commit()
}

// GetIterations returns iterations for a session, optionally filtered by stage.
func (s *Store) GetIterations(ctx context.Context, sessionName string, stageFilter string) ([]IterationRow, error) {
	var query string
	var args []any
	if stageFilter != "" {
		query = `SELECT id, session_name, stage_name, iteration, status, decision, summary,
		                exit_code, signals_json, started_at, completed_at
		         FROM iterations WHERE session_name = ? AND stage_name = ? ORDER BY id`
		args = []any{sessionName, stageFilter}
	} else {
		query = `SELECT id, session_name, stage_name, iteration, status, decision, summary,
		                exit_code, signals_json, started_at, completed_at
		         FROM iterations WHERE session_name = ? ORDER BY id`
		args = []any{sessionName}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: get iterations: %w", err)
	}
	defer rows.Close()

	var result []IterationRow
	for rows.Next() {
		var r IterationRow
		if err := rows.Scan(
			&r.ID, &r.SessionName, &r.StageName, &r.Iteration, &r.Status,
			&r.Decision, &r.Summary, &r.ExitCode, &r.SignalsJSON,
			&r.StartedAt, &r.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("store: get iterations scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
