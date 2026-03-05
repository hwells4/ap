package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AcquireLock attempts to insert a lock row. Fails if already locked.
func (s *Store) AcquireLock(ctx context.Context, sessionName, holder string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO locks (session_name, holder, acquired_at) VALUES (?, ?, ?)",
		sessionName, holder, now,
	)
	if err != nil {
		return fmt.Errorf("store: acquire lock: %w", err)
	}
	return nil
}

// ReleaseLock removes the lock for a session.
func (s *Store) ReleaseLock(ctx context.Context, sessionName string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM locks WHERE session_name = ?", sessionName)
	if err != nil {
		return fmt.Errorf("store: release lock: %w", err)
	}
	return nil
}

// CheckLock returns the holder and whether a lock exists.
func (s *Store) CheckLock(ctx context.Context, sessionName string) (string, bool, error) {
	var holder string
	err := s.db.QueryRowContext(ctx,
		"SELECT holder FROM locks WHERE session_name = ?", sessionName,
	).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: check lock: %w", err)
	}
	return holder, true, nil
}
