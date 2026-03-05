package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// EventRow represents a row from the events table.
type EventRow struct {
	ID          int64
	SessionName string
	Seq         int
	Type        string
	CursorJSON  string
	DataJSON    string
	CreatedAt   string
}

// AppendEvent appends an event with the next monotonic seq for the session.
// It wraps the operation in a transaction for atomicity.
func (s *Store) AppendEvent(ctx context.Context, sessionName, eventType, cursorJSON, dataJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: append event begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := appendEventTx(ctx, tx, sessionName, eventType, cursorJSON, dataJSON); err != nil {
		return err
	}
	return tx.Commit()
}

// appendEventTx appends an event inside an existing transaction.
func appendEventTx(ctx context.Context, tx *sql.Tx, sessionName, eventType, cursorJSON, dataJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var nextSeq int
	err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE session_name = ?",
		sessionName,
	).Scan(&nextSeq)
	if err != nil {
		return fmt.Errorf("store: next seq: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO events (session_name, seq, type, cursor_json, data_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sessionName, nextSeq, eventType, cursorJSON, dataJSON, now,
	)
	if err != nil {
		return fmt.Errorf("store: append event: %w", err)
	}
	return nil
}

// GetEvents returns events for a session with optional type filter and afterSeq.
func (s *Store) GetEvents(ctx context.Context, sessionName string, typeFilter string, afterSeq int) ([]EventRow, error) {
	var query string
	var args []any

	if typeFilter != "" {
		query = `SELECT id, session_name, seq, type, cursor_json, data_json, created_at
		         FROM events WHERE session_name = ? AND type = ? AND seq > ? ORDER BY seq`
		args = []any{sessionName, typeFilter, afterSeq}
	} else {
		query = `SELECT id, session_name, seq, type, cursor_json, data_json, created_at
		         FROM events WHERE session_name = ? AND seq > ? ORDER BY seq`
		args = []any{sessionName, afterSeq}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: get events: %w", err)
	}
	defer rows.Close()

	var result []EventRow
	for rows.Next() {
		var r EventRow
		if err := rows.Scan(&r.ID, &r.SessionName, &r.Seq, &r.Type, &r.CursorJSON, &r.DataJSON, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: get events scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// TailEvents returns events with seq > afterSeq, ordered by seq.
func (s *Store) TailEvents(ctx context.Context, sessionName string, afterSeq int) ([]EventRow, error) {
	return s.GetEvents(ctx, sessionName, "", afterSeq)
}
