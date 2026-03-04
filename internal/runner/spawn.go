package runner

import (
	"fmt"
	"os"
	"strings"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/signals"
	"github.com/hwells4/ap/internal/spec"
	"github.com/hwells4/ap/internal/stage"
)

const (
	defaultMaxChildSessions = 10
	defaultMaxSpawnDepth    = 3
)

func processSpawnSignals(
	cfg Config,
	ew *events.Writer,
	iteration int,
	spawnSignals []signals.SpawnSignal,
	spawnedChildren int,
) (int, error) {
	if len(spawnSignals) == 0 {
		return spawnedChildren, nil
	}

	maxChildren := cfg.SpawnMaxChildren
	if maxChildren <= 0 {
		maxChildren = defaultMaxChildSessions
	}

	maxDepth := cfg.SpawnMaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxSpawnDepth
	}

	projectRoot, err := spawnProjectRoot(cfg.WorkDir)
	if err != nil {
		return spawnedChildren, err
	}

	cursor := &events.Cursor{
		Iteration: iteration,
		Provider:  cfg.Provider.Name(),
	}

	for idx, spawnSignal := range spawnSignals {
		failure := func(reason error) error {
			return ew.Append(events.NewEvent("signal.spawn.failed", cfg.Session, cursor, map[string]any{
				"iteration":     iteration,
				"signal_index":  idx,
				"run":           spawnSignal.Run,
				"child_session": spawnSignal.Session,
				"error":         reason.Error(),
			}))
		}

		if cfg.SpawnDepth >= maxDepth {
			if err := failure(fmt.Errorf("max_spawn_depth exceeded (%d)", maxDepth)); err != nil {
				return spawnedChildren, err
			}
			continue
		}

		if spawnedChildren >= maxChildren {
			if err := failure(fmt.Errorf("max_child_sessions reached (%d)", maxChildren)); err != nil {
				return spawnedChildren, err
			}
			continue
		}

		if cfg.Launcher == nil {
			if err := failure(session.ErrLauncherRequired); err != nil {
				return spawnedChildren, err
			}
			continue
		}

		parsed, err := spec.ParseWithOptions(spawnSignal.Run, spec.ParseOptions{
			StageResolveOpts: stage.ResolveOptions{ProjectRoot: projectRoot},
		})
		if err != nil {
			if appendErr := failure(fmt.Errorf("parse run spec: %w", err)); appendErr != nil {
				return spawnedChildren, appendErr
			}
			continue
		}

		parsed, err = applySpawnCountOverride(parsed, spawnSignal.N)
		if err != nil {
			if appendErr := failure(err); appendErr != nil {
				return spawnedChildren, appendErr
			}
			continue
		}

		childSession, err := session.Start(parsed, spawnSignal.Session, session.StartOpts{
			ProjectRoot:   projectRoot,
			Provider:      cfg.Provider.Name(),
			Model:         cfg.Model,
			Context:       spawnSignal.Context,
			ParentSession: cfg.Session,
			Executable:    cfg.ExecutablePath,
			Launcher:      cfg.Launcher,
			LauncherOpts: session.StartOptions{
				WorkDir: projectRoot,
			},
		})
		if err != nil {
			if appendErr := failure(fmt.Errorf("start child session: %w", err)); appendErr != nil {
				return spawnedChildren, appendErr
			}
			continue
		}

		spawnedChildren++
		if err := ew.Append(events.NewEvent("signal.spawn", cfg.Session, cursor, map[string]any{
			"iteration":     iteration,
			"signal_index":  idx,
			"run":           spawnSignal.Run,
			"child_session": childSession.Name,
			"child_run_dir": childSession.RunDir,
			"pid":           childSession.Handle.PID,
			"backend":       childSession.Handle.Backend,
		})); err != nil {
			return spawnedChildren, err
		}
	}

	return spawnedChildren, nil
}

func applySpawnCountOverride(parsed spec.Spec, override int) (spec.Spec, error) {
	if override <= 0 {
		return parsed, nil
	}

	stageSpec, ok := parsed.(spec.StageSpec)
	if !ok {
		return nil, fmt.Errorf("spawn n override is only supported for stage specs")
	}
	stageSpec.Iterations = override
	return stageSpec, nil
}

func spawnProjectRoot(workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir != "" {
		return workDir, nil
	}
	return os.Getwd()
}
