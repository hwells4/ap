package store

import (
	"context"
	"fmt"
)

// AddChild records a parent-child session relationship.
func (s *Store) AddChild(ctx context.Context, parent, child string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO session_children (parent_name, child_name) VALUES (?, ?)",
		parent, child,
	)
	if err != nil {
		return fmt.Errorf("store: add child: %w", err)
	}
	return nil
}

// GetChildren returns child session names for a parent, ordered alphabetically.
func (s *Store) GetChildren(ctx context.Context, parent string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT child_name FROM session_children WHERE parent_name = ? ORDER BY child_name",
		parent,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get children: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("store: get children scan: %w", err)
		}
		result = append(result, name)
	}
	return result, rows.Err()
}
