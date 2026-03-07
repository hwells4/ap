package store

import (
	"context"
	"encoding/json"
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
	StreamJSON   string
}

// IterationRow represents a row from the iterations table.
type IterationRow struct {
	ID           int64
	SessionName  string
	StageName    string
	Iteration    int
	Status       string
	Decision     string
	Summary      string
	ExitCode     int
	SignalsJSON  string
	StartedAt    string
	CompletedAt  *string
	DurationMS   int64
	WorkManifest string
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

	cursorBytes, err := json.Marshal(struct {
		Iteration int    `json:"iteration"`
		Provider  string `json:"provider"`
	}{input.Iteration, input.ProviderName})
	if err != nil {
		return fmt.Errorf("store: marshal cursor json: %w", err)
	}
	dataBytes, err := json.Marshal(struct {
		Stage     string `json:"stage"`
		Iteration int    `json:"iteration"`
	}{input.StageName, input.Iteration})
	if err != nil {
		return fmt.Errorf("store: marshal data json: %w", err)
	}
	if err := appendEventTx(ctx, tx, input.SessionName, "iteration.started", string(cursorBytes), string(dataBytes)); err != nil {
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
		SET status = 'completed', decision = ?, summary = ?, exit_code = ?, signals_json = ?, completed_at = ?, duration_ms = ?
		WHERE session_name = ? AND stage_name = ? AND iteration = ?
		RETURNING id`,
		input.Decision, input.Summary, input.ExitCode, input.SignalsJSON, now, input.DurationMS,
		input.SessionName, input.StageName, input.Iteration,
	).Scan(&iterID)
	if err != nil {
		return fmt.Errorf("store: update iteration: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO outputs (iteration_id, stdout, stderr, context_json, stream_json)
		VALUES (?, ?, ?, ?, ?)`,
		iterID, input.Stdout, input.Stderr, input.ContextJSON, input.StreamJSON,
	)
	if err != nil {
		return fmt.Errorf("store: insert output: %w", err)
	}

	cursorBytes2, err := json.Marshal(struct {
		Iteration int    `json:"iteration"`
		Provider  string `json:"provider"`
	}{input.Iteration, input.ProviderName})
	if err != nil {
		return fmt.Errorf("store: marshal cursor json: %w", err)
	}
	dataBytes2, err := json.Marshal(struct {
		Stage     string `json:"stage"`
		Iteration int    `json:"iteration"`
		Decision  string `json:"decision"`
		Summary   string `json:"summary"`
		Duration  int64  `json:"duration"`
	}{input.StageName, input.Iteration, input.Decision, input.Summary, input.DurationMS})
	if err != nil {
		return fmt.Errorf("store: marshal data json: %w", err)
	}
	if err := appendEventTx(ctx, tx, input.SessionName, "iteration.completed", string(cursorBytes2), string(dataBytes2)); err != nil {
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

// CleanOrphanedIterations finds iterations stuck in "started" status for the
// given session (caused by a crash between StartIteration and CompleteIteration),
// marks them as "failed", and inserts a placeholder output row for any that
// lack one (to maintain the 1:1 iteration/output pairing). Returns the count
// of cleaned iterations.
func (s *Store) CleanOrphanedIterations(ctx context.Context, sessionName string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: clean orphaned begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	// Find orphaned iteration IDs.
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM iterations WHERE session_name = ? AND status = 'started'`,
		sessionName,
	)
	if err != nil {
		return 0, fmt.Errorf("store: query orphaned iterations: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: scan orphaned iteration id: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: rows err orphaned iterations: %w", err)
	}

	if len(ids) == 0 {
		return 0, tx.Commit()
	}

	// Mark each orphaned iteration as failed and ensure an output row exists.
	for _, id := range ids {
		_, err = tx.ExecContext(ctx,
			`UPDATE iterations SET status = 'failed', completed_at = ? WHERE id = ?`,
			now, id,
		)
		if err != nil {
			return 0, fmt.Errorf("store: update orphaned iteration %d: %w", id, err)
		}

		// Insert output row only if one doesn't already exist.
		_, err = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO outputs (iteration_id, stdout, stderr, context_json) VALUES (?, '', '', '{}')`,
			id,
		)
		if err != nil {
			return 0, fmt.Errorf("store: insert orphaned output %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: clean orphaned commit: %w", err)
	}
	return len(ids), nil
}

// GetIterations returns iterations for a session, optionally filtered by stage.
func (s *Store) GetIterations(ctx context.Context, sessionName string, stageFilter string) ([]IterationRow, error) {
	var query string
	var args []any
	if stageFilter != "" {
		query = `SELECT i.id, i.session_name, i.stage_name, i.iteration, i.status,
		                i.decision, i.summary, i.exit_code, i.signals_json,
		                i.started_at, i.completed_at, i.duration_ms,
		                COALESCE(o.context_json, '{}') AS work_manifest
		         FROM iterations i
		         LEFT JOIN outputs o ON o.iteration_id = i.id
		         WHERE i.session_name = ? AND i.stage_name = ? ORDER BY i.id`
		args = []any{sessionName, stageFilter}
	} else {
		query = `SELECT i.id, i.session_name, i.stage_name, i.iteration, i.status,
		                i.decision, i.summary, i.exit_code, i.signals_json,
		                i.started_at, i.completed_at, i.duration_ms,
		                COALESCE(o.context_json, '{}') AS work_manifest
		         FROM iterations i
		         LEFT JOIN outputs o ON o.iteration_id = i.id
		         WHERE i.session_name = ? ORDER BY i.id`
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
			&r.StartedAt, &r.CompletedAt, &r.DurationMS, &r.WorkManifest,
		); err != nil {
			return nil, fmt.Errorf("store: get iterations scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
