package controlplane

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const envControlDBPath = "AP_CONTROL_DB"

const ddl = `
CREATE TABLE IF NOT EXISTS projects (
    project_key   TEXT PRIMARY KEY,
    project_root  TEXT NOT NULL,
    db_path       TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS session_index (
    project_key          TEXT NOT NULL,
    project_root         TEXT NOT NULL,
    session_name         TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'running',
    iteration            INTEGER NOT NULL DEFAULT 0,
    iteration_completed  INTEGER NOT NULL DEFAULT 0,
    started_at           TEXT NOT NULL DEFAULT '',
    completed_at         TEXT,
    current_stage        TEXT NOT NULL DEFAULT '',
    repo_root            TEXT NOT NULL DEFAULT '',
    config_root          TEXT NOT NULL DEFAULT '',
    target_source        TEXT NOT NULL DEFAULT '',
    updated_at           TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project_key, session_name)
);

CREATE INDEX IF NOT EXISTS idx_session_index_session_name
    ON session_index(session_name);
CREATE INDEX IF NOT EXISTS idx_session_index_status
    ON session_index(status);
`

// SessionRecord is the machine-level session index row.
type SessionRecord struct {
	ProjectKey         string
	ProjectRoot        string
	SessionName        string
	Status             string
	Iteration          int
	IterationCompleted int
	StartedAt          string
	CompletedAt        *string
	CurrentStage       string
	RepoRoot           string
	ConfigRoot         string
	TargetSource       string
	UpdatedAt          string
}

// DB is the global control-plane store.
type DB struct {
	db   *sql.DB
	path string
}

// DefaultPath returns the control DB path for this machine.
func DefaultPath() (string, error) {
	if overridden := strings.TrimSpace(os.Getenv(envControlDBPath)); overridden != "" {
		return overridden, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("control-plane: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "ap", "control.db"), nil
}

// Open opens the global control-plane database. When path is empty, DefaultPath is used.
func Open(path string) (*DB, error) {
	resolved := strings.TrimSpace(path)
	if resolved == "" {
		var err error
		resolved, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	if resolved != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return nil, fmt.Errorf("control-plane: create dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", resolved)
	if err != nil {
		return nil, fmt.Errorf("control-plane: open: %w", err)
	}
	cp := &DB{db: db, path: resolved}
	if err := cp.setPragmas(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := cp.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return cp, nil
}

// Close closes the underlying database.
func (c *DB) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// Path returns the on-disk path to the control DB.
func (c *DB) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

func (c *DB) setPragmas() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := c.db.Exec(p); err != nil {
			return fmt.Errorf("control-plane: pragma %q: %w", p, err)
		}
	}
	return nil
}

func (c *DB) migrate() error {
	if _, err := c.db.Exec(ddl); err != nil {
		return fmt.Errorf("control-plane: migrate: %w", err)
	}
	return nil
}

// UpsertProject records a project store location.
func (c *DB) UpsertProject(projectRoot, projectKey, dbPath string) error {
	if c == nil || c.db == nil {
		return nil
	}
	projectRoot = strings.TrimSpace(projectRoot)
	projectKey = strings.TrimSpace(projectKey)
	dbPath = strings.TrimSpace(dbPath)
	if projectRoot == "" || projectKey == "" || dbPath == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := c.db.Exec(`
		INSERT INTO projects (project_key, project_root, db_path, last_seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(project_key) DO UPDATE SET
			project_root = excluded.project_root,
			db_path = excluded.db_path,
			last_seen_at = excluded.last_seen_at
	`, projectKey, projectRoot, dbPath, now)
	if err != nil {
		return fmt.Errorf("control-plane: upsert project: %w", err)
	}
	return nil
}

// UpsertSession writes current session state to the global index.
func (c *DB) UpsertSession(rec SessionRecord) error {
	if c == nil || c.db == nil {
		return nil
	}
	rec.ProjectKey = strings.TrimSpace(rec.ProjectKey)
	rec.ProjectRoot = strings.TrimSpace(rec.ProjectRoot)
	rec.SessionName = strings.TrimSpace(rec.SessionName)
	if rec.ProjectKey == "" || rec.ProjectRoot == "" || rec.SessionName == "" {
		return nil
	}
	if strings.TrimSpace(rec.Status) == "" {
		rec.Status = "running"
	}
	if strings.TrimSpace(rec.UpdatedAt) == "" {
		rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := c.db.Exec(`
		INSERT INTO session_index (
			project_key, project_root, session_name, status,
			iteration, iteration_completed, started_at, completed_at,
			current_stage, repo_root, config_root, target_source, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_key, session_name) DO UPDATE SET
			project_root = excluded.project_root,
			status = excluded.status,
			iteration = excluded.iteration,
			iteration_completed = excluded.iteration_completed,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			current_stage = excluded.current_stage,
			repo_root = excluded.repo_root,
			config_root = excluded.config_root,
			target_source = excluded.target_source,
			updated_at = excluded.updated_at
	`,
		rec.ProjectKey, rec.ProjectRoot, rec.SessionName, rec.Status,
		rec.Iteration, rec.IterationCompleted, rec.StartedAt, rec.CompletedAt,
		rec.CurrentStage, rec.RepoRoot, rec.ConfigRoot, rec.TargetSource, rec.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("control-plane: upsert session: %w", err)
	}
	return nil
}

// DeleteSession removes a session from the global index.
func (c *DB) DeleteSession(projectKey, sessionName string) error {
	if c == nil || c.db == nil {
		return nil
	}
	projectKey = strings.TrimSpace(projectKey)
	sessionName = strings.TrimSpace(sessionName)
	if projectKey == "" || sessionName == "" {
		return nil
	}
	_, err := c.db.Exec(
		`DELETE FROM session_index WHERE project_key = ? AND session_name = ?`,
		projectKey, sessionName,
	)
	if err != nil {
		return fmt.Errorf("control-plane: delete session: %w", err)
	}
	return nil
}

// FindBySessionName returns all rows for a session name across projects.
func (c *DB) FindBySessionName(sessionName string) ([]SessionRecord, error) {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return nil, nil
	}
	rows, err := c.db.Query(`
		SELECT project_key, project_root, session_name, status,
		       iteration, iteration_completed, started_at, completed_at,
		       current_stage, repo_root, config_root, target_source, updated_at
		FROM session_index
		WHERE session_name = ?
		ORDER BY project_root, session_name
	`, sessionName)
	if err != nil {
		return nil, fmt.Errorf("control-plane: find session by name: %w", err)
	}
	defer rows.Close()
	return scanSessionRecords(rows)
}

// ListSessions returns session index rows, optionally filtered by status.
func (c *DB) ListSessions(statusFilter string) ([]SessionRecord, error) {
	statusFilter = strings.TrimSpace(statusFilter)
	var (
		rows *sql.Rows
		err  error
	)
	if statusFilter != "" {
		rows, err = c.db.Query(`
			SELECT project_key, project_root, session_name, status,
			       iteration, iteration_completed, started_at, completed_at,
			       current_stage, repo_root, config_root, target_source, updated_at
			FROM session_index
			WHERE status = ?
			ORDER BY project_root, session_name
		`, statusFilter)
	} else {
		rows, err = c.db.Query(`
			SELECT project_key, project_root, session_name, status,
			       iteration, iteration_completed, started_at, completed_at,
			       current_stage, repo_root, config_root, target_source, updated_at
			FROM session_index
			ORDER BY project_root, session_name
		`)
	}
	if err != nil {
		return nil, fmt.Errorf("control-plane: list sessions: %w", err)
	}
	defer rows.Close()
	return scanSessionRecords(rows)
}

func scanSessionRecords(rows *sql.Rows) ([]SessionRecord, error) {
	out := []SessionRecord{}
	for rows.Next() {
		var rec SessionRecord
		if err := rows.Scan(
			&rec.ProjectKey, &rec.ProjectRoot, &rec.SessionName, &rec.Status,
			&rec.Iteration, &rec.IterationCompleted, &rec.StartedAt, &rec.CompletedAt,
			&rec.CurrentStage, &rec.RepoRoot, &rec.ConfigRoot, &rec.TargetSource, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("control-plane: scan session index: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("control-plane: list sessions rows: %w", err)
	}
	return out, nil
}
