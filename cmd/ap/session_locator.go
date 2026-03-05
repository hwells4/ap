package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/controlplane"
	"github.com/hwells4/ap/internal/store"
)

var errSessionLookupNotFound = errors.New("session lookup: not found")

type sessionLookupAmbiguousError struct {
	SessionName string
	Matches     []controlplane.SessionRecord
}

func (e *sessionLookupAmbiguousError) Error() string {
	roots := make([]string, 0, len(e.Matches))
	for _, match := range e.Matches {
		if root := strings.TrimSpace(match.ProjectRoot); root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return fmt.Sprintf("session %q exists in multiple projects", e.SessionName)
	}
	return fmt.Sprintf("session %q exists in multiple projects: %s", e.SessionName, strings.Join(roots, ", "))
}

func resolveSessionStore(
	ctx context.Context,
	deps cliDeps,
	sessionName string,
	projectRootFlag string,
) (*store.Store, func(), error) {
	projectRootFlag = strings.TrimSpace(projectRootFlag)
	if projectRootFlag != "" {
		projectRoot, err := resolveRunProjectRoot(projectRootFlag, deps.getwd)
		if err != nil {
			return nil, nil, err
		}
		s, err := store.Open(filepath.Join(projectRoot, ".ap", "ap.db"))
		if err != nil {
			return nil, nil, fmt.Errorf("open store at %q: %w", projectRoot, err)
		}
		return s, func() { _ = s.Close() }, nil
	}

	if deps.store != nil {
		if _, err := deps.store.GetSession(ctx, sessionName); err == nil {
			return deps.store, func() {}, nil
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, nil, err
		}
	}

	cp, err := controlplane.Open("")
	if err != nil {
		return nil, nil, fmt.Errorf("open global session index: %w", err)
	}
	defer cp.Close()

	matches, err := cp.FindBySessionName(sessionName)
	if err != nil {
		return nil, nil, err
	}
	if len(matches) == 0 {
		return nil, nil, errSessionLookupNotFound
	}
	if len(matches) > 1 {
		return nil, nil, &sessionLookupAmbiguousError{
			SessionName: sessionName,
			Matches:     matches,
		}
	}

	target := strings.TrimSpace(matches[0].ProjectRoot)
	if target == "" {
		return nil, nil, errSessionLookupNotFound
	}
	if deps.store != nil && strings.TrimSpace(deps.store.ProjectRoot()) == target {
		return deps.store, func() {}, nil
	}

	s, err := store.Open(filepath.Join(target, ".ap", "ap.db"))
	if err != nil {
		return nil, nil, fmt.Errorf("open store at %q: %w", target, err)
	}
	return s, func() { _ = s.Close() }, nil
}
