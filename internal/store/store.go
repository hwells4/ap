package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hwells4/ap/internal/controlplane"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database for session state management.
type Store struct {
	db          *sql.DB
	path        string
	projectRoot string
	projectKey  string
	control     *controlplane.DB
}

// Open opens (or creates) the SQLite database at path.
// Use ":memory:" for testing.
func Open(path string) (*Store, error) {
	root, key := projectIdentityFromDBPath(path)

	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("store: create dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if path == ":memory:" {
		// In-memory databases are per-connection; limit to 1 so all
		// goroutines share the same underlying database.
		db.SetMaxOpenConns(1)
	}
	s := &Store{
		db:          db,
		path:        path,
		projectRoot: root,
		projectKey:  key,
	}
	if err := s.setPragmas(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	s.attachControlPlane()
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	var controlErr error
	if s.control != nil {
		controlErr = s.control.Close()
	}
	dbErr := s.db.Close()
	if dbErr != nil {
		return dbErr
	}
	return controlErr
}

// DB returns the underlying *sql.DB.
func (s *Store) DB() *sql.DB { return s.db }

// ProjectRoot returns the inferred project root for this store path.
func (s *Store) ProjectRoot() string { return s.projectRoot }

func (s *Store) setPragmas() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("store: pragma %q: %w", p, err)
		}
	}
	return nil
}

func (s *Store) attachControlPlane() {
	if s == nil || s.path == ":memory:" || s.projectRoot == "" || s.projectKey == "" {
		return
	}
	cp, err := controlplane.Open("")
	if err != nil {
		return
	}
	s.control = cp
	_ = s.control.UpsertProject(s.projectRoot, s.projectKey, s.path)
	s.syncAllSessionIndex(context.Background())
}

func projectIdentityFromDBPath(path string) (projectRoot string, projectKey string) {
	if path == "" || path == ":memory:" {
		return "", ""
	}
	clean := filepath.Clean(path)
	if filepath.Base(clean) != "ap.db" {
		return "", ""
	}
	apDir := filepath.Dir(clean)
	if filepath.Base(apDir) != ".ap" {
		return "", ""
	}
	root := filepath.Dir(apDir)
	if root == "." || root == string(os.PathSeparator) {
		return root, root
	}
	return root, filepath.Clean(root)
}
