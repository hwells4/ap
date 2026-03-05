package controlplane

import (
	"testing"
)

func mustOpen(t *testing.T) *DB {
	t.Helper()
	cp, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { cp.Close() })
	return cp
}

func TestOpenClose(t *testing.T) {
	cp, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	if cp.Path() != ":memory:" {
		t.Errorf("Path() = %q, want %q", cp.Path(), ":memory:")
	}
	if err := cp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Double-close should be safe (underlying sql.DB handles this).
	// A nil DB receiver should also be safe.
	var nilDB *DB
	if err := nilDB.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

func TestPathNilReceiver(t *testing.T) {
	var nilDB *DB
	if got := nilDB.Path(); got != "" {
		t.Errorf("nil Path() = %q, want empty", got)
	}
}

func TestUpsertProject(t *testing.T) {
	cp := mustOpen(t)

	// Insert a project.
	if err := cp.UpsertProject("/home/user/proj", "proj-key", "/home/user/proj/.ap/ap.db"); err != nil {
		t.Fatalf("UpsertProject insert: %v", err)
	}

	// Verify it exists by querying the DB directly.
	var projectRoot, dbPath string
	err := cp.db.QueryRow("SELECT project_root, db_path FROM projects WHERE project_key = ?", "proj-key").
		Scan(&projectRoot, &dbPath)
	if err != nil {
		t.Fatalf("query project: %v", err)
	}
	if projectRoot != "/home/user/proj" {
		t.Errorf("project_root = %q, want %q", projectRoot, "/home/user/proj")
	}
	if dbPath != "/home/user/proj/.ap/ap.db" {
		t.Errorf("db_path = %q, want %q", dbPath, "/home/user/proj/.ap/ap.db")
	}

	// Upsert same project key with different path — should update, not duplicate.
	if err := cp.UpsertProject("/home/user/proj2", "proj-key", "/home/user/proj2/.ap/ap.db"); err != nil {
		t.Fatalf("UpsertProject update: %v", err)
	}

	var count int
	err = cp.db.QueryRow("SELECT COUNT(*) FROM projects WHERE project_key = ?", "proj-key").Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after upsert, got %d", count)
	}

	err = cp.db.QueryRow("SELECT project_root, db_path FROM projects WHERE project_key = ?", "proj-key").
		Scan(&projectRoot, &dbPath)
	if err != nil {
		t.Fatalf("query after upsert: %v", err)
	}
	if projectRoot != "/home/user/proj2" {
		t.Errorf("project_root after upsert = %q, want %q", projectRoot, "/home/user/proj2")
	}
	if dbPath != "/home/user/proj2/.ap/ap.db" {
		t.Errorf("db_path after upsert = %q, want %q", dbPath, "/home/user/proj2/.ap/ap.db")
	}
}

func TestUpsertSession(t *testing.T) {
	cp := mustOpen(t)

	rec := SessionRecord{
		ProjectKey:  "pk1",
		ProjectRoot: "/root1",
		SessionName: "sess1",
		Status:      "running",
		Iteration:   1,
		StartedAt:   "2026-01-01T00:00:00Z",
	}
	if err := cp.UpsertSession(rec); err != nil {
		t.Fatalf("UpsertSession insert: %v", err)
	}

	// Verify via FindBySessionName.
	found, err := cp.FindBySessionName("sess1")
	if err != nil {
		t.Fatalf("FindBySessionName: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 session, got %d", len(found))
	}
	if found[0].Status != "running" {
		t.Errorf("Status = %q, want %q", found[0].Status, "running")
	}
	if found[0].Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", found[0].Iteration)
	}

	// Upsert same (project_key, session_name) with different status.
	rec.Status = "completed"
	rec.Iteration = 5
	if err := cp.UpsertSession(rec); err != nil {
		t.Fatalf("UpsertSession update: %v", err)
	}

	found, err = cp.FindBySessionName("sess1")
	if err != nil {
		t.Fatalf("FindBySessionName after update: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 session after upsert, got %d", len(found))
	}
	if found[0].Status != "completed" {
		t.Errorf("Status after upsert = %q, want %q", found[0].Status, "completed")
	}
	if found[0].Iteration != 5 {
		t.Errorf("Iteration after upsert = %d, want 5", found[0].Iteration)
	}
}

func TestUpsertSessionDefaultStatus(t *testing.T) {
	cp := mustOpen(t)

	// Empty status should default to "running".
	rec := SessionRecord{
		ProjectKey:  "pk1",
		ProjectRoot: "/root1",
		SessionName: "sess-default",
	}
	if err := cp.UpsertSession(rec); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	found, err := cp.FindBySessionName("sess-default")
	if err != nil {
		t.Fatalf("FindBySessionName: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 session, got %d", len(found))
	}
	if found[0].Status != "running" {
		t.Errorf("Status = %q, want %q (default)", found[0].Status, "running")
	}
}

func TestDeleteSession(t *testing.T) {
	cp := mustOpen(t)

	rec := SessionRecord{
		ProjectKey:  "pk1",
		ProjectRoot: "/root1",
		SessionName: "doomed",
		Status:      "running",
	}
	if err := cp.UpsertSession(rec); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	// Verify it exists.
	found, _ := cp.FindBySessionName("doomed")
	if len(found) != 1 {
		t.Fatalf("expected 1, got %d", len(found))
	}

	// Delete it.
	if err := cp.DeleteSession("pk1", "doomed"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Verify it's gone.
	found, err := cp.FindBySessionName("doomed")
	if err != nil {
		t.Fatalf("FindBySessionName after delete: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(found))
	}
}

func TestDeleteSessionNonexistent(t *testing.T) {
	cp := mustOpen(t)

	// Deleting a nonexistent session should not error.
	if err := cp.DeleteSession("no-project", "no-session"); err != nil {
		t.Fatalf("DeleteSession nonexistent: %v", err)
	}
}

func TestFindBySessionName_SingleMatch(t *testing.T) {
	cp := mustOpen(t)

	cp.UpsertSession(SessionRecord{
		ProjectKey: "pk1", ProjectRoot: "/root1", SessionName: "unique-sess", Status: "running",
	})

	found, err := cp.FindBySessionName("unique-sess")
	if err != nil {
		t.Fatalf("FindBySessionName: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 match, got %d", len(found))
	}
	if found[0].ProjectKey != "pk1" {
		t.Errorf("ProjectKey = %q, want %q", found[0].ProjectKey, "pk1")
	}
	if found[0].SessionName != "unique-sess" {
		t.Errorf("SessionName = %q, want %q", found[0].SessionName, "unique-sess")
	}
}

func TestFindBySessionName_MultipleProjects(t *testing.T) {
	cp := mustOpen(t)

	cp.UpsertSession(SessionRecord{
		ProjectKey: "projA", ProjectRoot: "/rootA", SessionName: "shared-name", Status: "running",
	})
	cp.UpsertSession(SessionRecord{
		ProjectKey: "projB", ProjectRoot: "/rootB", SessionName: "shared-name", Status: "completed",
	})

	found, err := cp.FindBySessionName("shared-name")
	if err != nil {
		t.Fatalf("FindBySessionName: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(found))
	}

	// Verify both projects are represented.
	keys := map[string]bool{}
	for _, r := range found {
		keys[r.ProjectKey] = true
	}
	if !keys["projA"] || !keys["projB"] {
		t.Errorf("expected both projA and projB, got keys %v", keys)
	}
}

func TestFindBySessionName_NotFound(t *testing.T) {
	cp := mustOpen(t)

	found, err := cp.FindBySessionName("nonexistent")
	if err != nil {
		t.Fatalf("FindBySessionName: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected 0, got %d", len(found))
	}
}

func TestFindBySessionName_EmptyString(t *testing.T) {
	cp := mustOpen(t)

	// Empty session name should return nil, nil.
	found, err := cp.FindBySessionName("")
	if err != nil {
		t.Fatalf("FindBySessionName empty: %v", err)
	}
	if found != nil {
		t.Errorf("expected nil for empty session name, got %v", found)
	}
}

func TestListSessions_All(t *testing.T) {
	cp := mustOpen(t)

	cp.UpsertSession(SessionRecord{
		ProjectKey: "pk1", ProjectRoot: "/root1", SessionName: "s1", Status: "running",
	})
	cp.UpsertSession(SessionRecord{
		ProjectKey: "pk1", ProjectRoot: "/root1", SessionName: "s2", Status: "completed",
	})
	cp.UpsertSession(SessionRecord{
		ProjectKey: "pk2", ProjectRoot: "/root2", SessionName: "s3", Status: "paused",
	})

	all, err := cp.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
}

func TestListSessions_WithStatusFilter(t *testing.T) {
	cp := mustOpen(t)

	cp.UpsertSession(SessionRecord{
		ProjectKey: "pk1", ProjectRoot: "/root1", SessionName: "s1", Status: "running",
	})
	cp.UpsertSession(SessionRecord{
		ProjectKey: "pk1", ProjectRoot: "/root1", SessionName: "s2", Status: "completed",
	})
	cp.UpsertSession(SessionRecord{
		ProjectKey: "pk2", ProjectRoot: "/root2", SessionName: "s3", Status: "running",
	})

	running, err := cp.ListSessions("running")
	if err != nil {
		t.Fatalf("ListSessions running: %v", err)
	}
	if len(running) != 2 {
		t.Fatalf("expected 2 running, got %d", len(running))
	}
	for _, r := range running {
		if r.Status != "running" {
			t.Errorf("expected status running, got %q", r.Status)
		}
	}

	completed, err := cp.ListSessions("completed")
	if err != nil {
		t.Fatalf("ListSessions completed: %v", err)
	}
	if len(completed) != 1 {
		t.Fatalf("expected 1 completed, got %d", len(completed))
	}
	if completed[0].SessionName != "s2" {
		t.Errorf("completed session = %q, want %q", completed[0].SessionName, "s2")
	}

	// Filter for a status with no matches.
	none, err := cp.ListSessions("failed")
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 for status 'failed', got %d", len(none))
	}
}

func TestListSessions_Empty(t *testing.T) {
	cp := mustOpen(t)

	all, err := cp.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions empty DB: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 sessions in empty DB, got %d", len(all))
	}
}

func TestSchemaMigrationIdempotent(t *testing.T) {
	cp := mustOpen(t)

	// Run migrate again — should be a no-op since tables use IF NOT EXISTS.
	if err := cp.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// The store should still work after double migration.
	if err := cp.UpsertProject("/root", "key", "/root/.ap/ap.db"); err != nil {
		t.Fatalf("UpsertProject after double migrate: %v", err)
	}
	rec := SessionRecord{
		ProjectKey: "key", ProjectRoot: "/root", SessionName: "test", Status: "running",
	}
	if err := cp.UpsertSession(rec); err != nil {
		t.Fatalf("UpsertSession after double migrate: %v", err)
	}
}

func TestSchemaMigrationIdempotent_SeparateOpens(t *testing.T) {
	// Simulate restart: open same file-based DB twice.
	tmp := t.TempDir()
	dbPath := tmp + "/control.db"

	cp1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	cp1.UpsertProject("/root", "key1", "/root/.ap/ap.db")
	cp1.Close()

	cp2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer cp2.Close()

	// Data from first open should still be there.
	var count int
	err = cp2.db.QueryRow("SELECT COUNT(*) FROM projects WHERE project_key = ?", "key1").Scan(&count)
	if err != nil {
		t.Fatalf("query after reopen: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 project after reopen, got %d", count)
	}
}

func TestEmptyInputs(t *testing.T) {
	cp := mustOpen(t)

	// UpsertProject with empty strings should be a no-op (returns nil).
	if err := cp.UpsertProject("", "", ""); err != nil {
		t.Fatalf("UpsertProject empty: %v", err)
	}
	if err := cp.UpsertProject("/root", "", "/db"); err != nil {
		t.Fatalf("UpsertProject empty key: %v", err)
	}
	if err := cp.UpsertProject("", "key", "/db"); err != nil {
		t.Fatalf("UpsertProject empty root: %v", err)
	}
	if err := cp.UpsertProject("/root", "key", ""); err != nil {
		t.Fatalf("UpsertProject empty dbPath: %v", err)
	}

	// Verify nothing was inserted.
	var count int
	cp.db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 projects after empty upserts, got %d", count)
	}

	// UpsertSession with empty required fields should be a no-op.
	if err := cp.UpsertSession(SessionRecord{}); err != nil {
		t.Fatalf("UpsertSession empty: %v", err)
	}
	if err := cp.UpsertSession(SessionRecord{ProjectKey: "pk"}); err != nil {
		t.Fatalf("UpsertSession only key: %v", err)
	}
	if err := cp.UpsertSession(SessionRecord{ProjectKey: "pk", ProjectRoot: "/r"}); err != nil {
		t.Fatalf("UpsertSession no session name: %v", err)
	}

	// Verify nothing was inserted.
	cp.db.QueryRow("SELECT COUNT(*) FROM session_index").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 sessions after empty upserts, got %d", count)
	}

	// DeleteSession with empty strings should be a no-op.
	if err := cp.DeleteSession("", ""); err != nil {
		t.Fatalf("DeleteSession empty: %v", err)
	}
	if err := cp.DeleteSession("pk", ""); err != nil {
		t.Fatalf("DeleteSession empty name: %v", err)
	}
	if err := cp.DeleteSession("", "sess"); err != nil {
		t.Fatalf("DeleteSession empty key: %v", err)
	}
}

func TestNilReceiverSafety(t *testing.T) {
	var cp *DB

	// All methods on nil receiver should return nil.
	if err := cp.UpsertProject("/root", "key", "/db"); err != nil {
		t.Errorf("nil UpsertProject: %v", err)
	}
	if err := cp.UpsertSession(SessionRecord{ProjectKey: "k", ProjectRoot: "/r", SessionName: "s"}); err != nil {
		t.Errorf("nil UpsertSession: %v", err)
	}
	if err := cp.DeleteSession("k", "s"); err != nil {
		t.Errorf("nil DeleteSession: %v", err)
	}
	if err := cp.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
	if p := cp.Path(); p != "" {
		t.Errorf("nil Path() = %q, want empty", p)
	}
}

func TestSessionRecordFields(t *testing.T) {
	cp := mustOpen(t)

	completed := "2026-01-02T00:00:00Z"
	rec := SessionRecord{
		ProjectKey:         "pk1",
		ProjectRoot:        "/root1",
		SessionName:        "full-record",
		Status:             "completed",
		Iteration:          10,
		IterationCompleted: 10,
		StartedAt:          "2026-01-01T00:00:00Z",
		CompletedAt:        &completed,
		CurrentStage:       "deploy",
		RepoRoot:           "/repo",
		ConfigRoot:         "/config",
		TargetSource:       "pipeline.yaml",
		UpdatedAt:          "2026-01-02T01:00:00Z",
	}
	if err := cp.UpsertSession(rec); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	found, err := cp.FindBySessionName("full-record")
	if err != nil {
		t.Fatalf("FindBySessionName: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1, got %d", len(found))
	}
	got := found[0]
	if got.ProjectKey != "pk1" {
		t.Errorf("ProjectKey = %q", got.ProjectKey)
	}
	if got.ProjectRoot != "/root1" {
		t.Errorf("ProjectRoot = %q", got.ProjectRoot)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Iteration != 10 {
		t.Errorf("Iteration = %d", got.Iteration)
	}
	if got.IterationCompleted != 10 {
		t.Errorf("IterationCompleted = %d", got.IterationCompleted)
	}
	if got.StartedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("StartedAt = %q", got.StartedAt)
	}
	if got.CompletedAt == nil || *got.CompletedAt != "2026-01-02T00:00:00Z" {
		t.Errorf("CompletedAt = %v", got.CompletedAt)
	}
	if got.CurrentStage != "deploy" {
		t.Errorf("CurrentStage = %q", got.CurrentStage)
	}
	if got.RepoRoot != "/repo" {
		t.Errorf("RepoRoot = %q", got.RepoRoot)
	}
	if got.ConfigRoot != "/config" {
		t.Errorf("ConfigRoot = %q", got.ConfigRoot)
	}
	if got.TargetSource != "pipeline.yaml" {
		t.Errorf("TargetSource = %q", got.TargetSource)
	}
	if got.UpdatedAt != "2026-01-02T01:00:00Z" {
		t.Errorf("UpdatedAt = %q", got.UpdatedAt)
	}
}

func TestOpenWithTempDir(t *testing.T) {
	tmp := t.TempDir()
	dbPath := tmp + "/subdir/control.db"

	cp, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open with subdir: %v", err)
	}
	defer cp.Close()

	if cp.Path() != dbPath {
		t.Errorf("Path() = %q, want %q", cp.Path(), dbPath)
	}

	// Verify it's functional.
	if err := cp.UpsertProject("/r", "k", "/d"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}
