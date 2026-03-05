package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/controlplane"
	"github.com/hwells4/ap/internal/store"
)

func TestResolveSessionStore_ProjectRootFlag(t *testing.T) {
	// When --project-root is provided, resolveSessionStore should open a
	// store at that exact path, ignoring deps.store and controlplane.
	projectRoot := t.TempDir()
	ctx := context.Background()

	// Pre-create a store at the target project root so Open succeeds.
	s, err := store.Open(filepath.Join(projectRoot, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "target-session", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// deps.store is nil — the flag path should be used directly.
	deps := cliDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
	}

	got, cleanup, err := resolveSessionStore(ctx, deps, "target-session", projectRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Verify the returned store can find the session we created.
	row, err := got.GetSession(ctx, "target-session")
	if err != nil {
		t.Fatalf("expected session in project-root store: %v", err)
	}
	if row.Name != "target-session" {
		t.Fatalf("session name = %q, want target-session", row.Name)
	}
}

func TestResolveSessionStore_LocalStoreHit(t *testing.T) {
	// When deps.store contains the session, it should be returned directly
	// without opening a new store.
	ctx := context.Background()

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateSession(ctx, "local-session", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}

	deps := cliDeps{
		store: s,
		getwd: func() (string, error) { return t.TempDir(), nil },
	}

	got, cleanup, err := resolveSessionStore(ctx, deps, "local-session", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cleanup()

	// Should return the exact same store pointer.
	if got != s {
		t.Fatal("expected resolveSessionStore to return deps.store for local hit")
	}
}

func TestResolveSessionStore_LocalStoreMiss_FallsToControlplane(t *testing.T) {
	// When the local store doesn't have the session but the controlplane
	// has a single match, it should open the store at that project root.
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	ctx := context.Background()

	// Create a store at a remote project root and register it in the controlplane.
	remoteRoot := t.TempDir()
	remoteStore, err := store.Open(filepath.Join(remoteRoot, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteStore.CreateSession(ctx, "remote-session", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := remoteStore.UpdateSession(ctx, "remote-session", map[string]any{
		"project_root": remoteRoot,
	}); err != nil {
		t.Fatal(err)
	}
	_ = remoteStore.Close()

	// Local store has no sessions.
	localStore, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer localStore.Close()

	deps := cliDeps{
		store: localStore,
		getwd: func() (string, error) { return t.TempDir(), nil },
	}

	got, cleanup, err := resolveSessionStore(ctx, deps, "remote-session", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	row, err := got.GetSession(ctx, "remote-session")
	if err != nil {
		t.Fatalf("expected session in remote store: %v", err)
	}
	if row.Name != "remote-session" {
		t.Fatalf("session name = %q, want remote-session", row.Name)
	}
}

func TestResolveSessionStore_AmbiguousMatch(t *testing.T) {
	// When the controlplane returns multiple matches, the function should
	// return a sessionLookupAmbiguousError.
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	ctx := context.Background()

	// Create two stores in different project roots with the same session name.
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		s, err := store.Open(filepath.Join(root, ".ap", "ap.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CreateSession(ctx, "dup-session", "loop", "", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateSession(ctx, "dup-session", map[string]any{
			"project_root": root,
		}); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}

	deps := cliDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
	}

	_, _, err := resolveSessionStore(ctx, deps, "dup-session", "")
	if err == nil {
		t.Fatal("expected error for ambiguous session, got nil")
	}

	var ambigErr *sessionLookupAmbiguousError
	if !errors.As(err, &ambigErr) {
		t.Fatalf("expected sessionLookupAmbiguousError, got %T: %v", err, err)
	}
	if ambigErr.SessionName != "dup-session" {
		t.Fatalf("ambiguous error session name = %q, want dup-session", ambigErr.SessionName)
	}
	if len(ambigErr.Matches) != 2 {
		t.Fatalf("ambiguous error matches = %d, want 2", len(ambigErr.Matches))
	}
}

func TestResolveSessionStore_NotFound(t *testing.T) {
	// When no store or controlplane has the session, errSessionLookupNotFound
	// should be returned.
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	ctx := context.Background()

	localStore, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer localStore.Close()

	deps := cliDeps{
		store: localStore,
		getwd: func() (string, error) { return t.TempDir(), nil },
	}

	_, _, err = resolveSessionStore(ctx, deps, "nonexistent", "")
	if !errors.Is(err, errSessionLookupNotFound) {
		t.Fatalf("expected errSessionLookupNotFound, got %v", err)
	}
}

func TestResolveSessionStore_EmptyProjectRoot(t *testing.T) {
	// A whitespace-only --project-root should be treated as empty (not used),
	// falling through to the local store check.
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	ctx := context.Background()

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateSession(ctx, "ws-session", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}

	deps := cliDeps{
		store: s,
		getwd: func() (string, error) { return t.TempDir(), nil },
	}

	got, cleanup, err := resolveSessionStore(ctx, deps, "ws-session", "   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cleanup()

	if got != s {
		t.Fatal("expected deps.store to be returned when project root is whitespace-only")
	}
}

func TestSessionLookupAmbiguousError_Message(t *testing.T) {
	t.Run("with project roots", func(t *testing.T) {
		err := &sessionLookupAmbiguousError{
			SessionName: "my-session",
			Matches: []controlplane.SessionRecord{
				{ProjectRoot: "/project/alpha"},
				{ProjectRoot: "/project/beta"},
			},
		}
		msg := err.Error()
		if msg != `session "my-session" exists in multiple projects: /project/alpha, /project/beta` {
			t.Fatalf("unexpected error message: %s", msg)
		}
	})

	t.Run("without project roots", func(t *testing.T) {
		err := &sessionLookupAmbiguousError{
			SessionName: "my-session",
			Matches: []controlplane.SessionRecord{
				{ProjectRoot: ""},
				{ProjectRoot: "   "},
			},
		}
		msg := err.Error()
		if msg != `session "my-session" exists in multiple projects` {
			t.Fatalf("unexpected error message: %s", msg)
		}
	})

	t.Run("mixed project roots", func(t *testing.T) {
		err := &sessionLookupAmbiguousError{
			SessionName: "my-session",
			Matches: []controlplane.SessionRecord{
				{ProjectRoot: "/project/alpha"},
				{ProjectRoot: ""},
			},
		}
		msg := err.Error()
		if msg != `session "my-session" exists in multiple projects: /project/alpha` {
			t.Fatalf("unexpected error message: %s", msg)
		}
	})
}
