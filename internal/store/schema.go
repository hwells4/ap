package store

import (
	"database/sql"
	"fmt"
)

const schemaVersion = 3

const ddl = `
CREATE TABLE IF NOT EXISTS sessions (
    name              TEXT PRIMARY KEY,
    type              TEXT NOT NULL DEFAULT '',
    pipeline          TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'running',
    node_id           TEXT NOT NULL DEFAULT '',
    iteration         INTEGER NOT NULL DEFAULT 0,
    iteration_completed INTEGER NOT NULL DEFAULT 0,
    started_at        TEXT NOT NULL DEFAULT '',
    completed_at      TEXT,
    current_stage     TEXT NOT NULL DEFAULT '',
    stages_json       TEXT NOT NULL DEFAULT '[]',
    history_json      TEXT NOT NULL DEFAULT '[]',
    error             TEXT,
    error_type        TEXT,
    escalation_json   TEXT,
	    parent_session    TEXT NOT NULL DEFAULT '',
	    child_sessions_json TEXT NOT NULL DEFAULT '[]',
	    run_request_json  TEXT NOT NULL DEFAULT '{}',
	    project_root      TEXT NOT NULL DEFAULT '',
	    repo_root         TEXT NOT NULL DEFAULT '',
	    config_root       TEXT NOT NULL DEFAULT '',
	    project_key       TEXT NOT NULL DEFAULT '',
	    target_source     TEXT NOT NULL DEFAULT '',
	    created_at        TEXT NOT NULL DEFAULT '',
	    updated_at        TEXT NOT NULL DEFAULT ''
	);

CREATE TABLE IF NOT EXISTS iterations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_name    TEXT NOT NULL,
    stage_name      TEXT NOT NULL DEFAULT '',
    iteration       INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'started',
    decision        TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    exit_code       INTEGER NOT NULL DEFAULT 0,
    signals_json    TEXT NOT NULL DEFAULT '{}',
    started_at      TEXT NOT NULL DEFAULT '',
    completed_at    TEXT,
    UNIQUE(session_name, stage_name, iteration),
    FOREIGN KEY (session_name) REFERENCES sessions(name) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS outputs (
    iteration_id  INTEGER PRIMARY KEY,
    stdout        TEXT NOT NULL DEFAULT '',
    stderr        TEXT NOT NULL DEFAULT '',
    context_json  TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (iteration_id) REFERENCES iterations(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    session_name  TEXT NOT NULL,
    seq           INTEGER NOT NULL,
    type          TEXT NOT NULL,
    cursor_json   TEXT NOT NULL DEFAULT '{}',
    data_json     TEXT NOT NULL DEFAULT '{}',
    created_at    TEXT NOT NULL DEFAULT '',
    UNIQUE(session_name, seq),
    FOREIGN KEY (session_name) REFERENCES sessions(name) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS locks (
    session_name  TEXT PRIMARY KEY,
    holder        TEXT NOT NULL DEFAULT '',
    acquired_at   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS session_children (
    parent_name   TEXT NOT NULL,
    child_name    TEXT NOT NULL,
    PRIMARY KEY (parent_name, child_name)
);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

	CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_name, seq);
	CREATE INDEX IF NOT EXISTS idx_events_session_type ON events(session_name, type);
	CREATE INDEX IF NOT EXISTS idx_iterations_session ON iterations(session_name);
	`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("store: migrate ddl: %w", err)
	}
	if err := s.ensureSessionColumn("project_root", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn("repo_root", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn("config_root", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn("project_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureSessionColumn("target_source", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_project_key ON sessions(project_key)"); err != nil {
		return fmt.Errorf("store: migrate sessions project_key index: %w", err)
	}
	if err := s.ensureColumn("iterations", "duration_ms", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	// Upsert schema version
	_, err := s.db.Exec("DELETE FROM schema_version")
	if err != nil {
		return fmt.Errorf("store: migrate version clear: %w", err)
	}
	_, err = s.db.Exec("INSERT INTO schema_version (version) VALUES (?)", schemaVersion)
	if err != nil {
		return fmt.Errorf("store: migrate version set: %w", err)
	}
	return nil
}

func (s *Store) ensureSessionColumn(name, ddlType string) error {
	return s.ensureColumn("sessions", name, ddlType)
}

func (s *Store) ensureColumn(table, name, ddlType string) error {
	has, err := s.tableHasColumn(table, name)
	if err != nil {
		return fmt.Errorf("store: inspect column %s.%s: %w", table, name, err)
	}
	if has {
		return nil
	}
	query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, ddlType)
	if _, err := s.db.Exec(query); err != nil {
		return fmt.Errorf("store: add %s.%s: %w", table, name, err)
	}
	return nil
}

func (s *Store) tableHasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultV   sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultV, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}
