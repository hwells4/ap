package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hwells4/ap/internal/runtarget"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/signals"
	"github.com/hwells4/ap/internal/spec"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/internal/store"
)

const (
	defaultMaxChildSessions = 10
	defaultMaxSpawnDepth    = 3
)

// spawnResult carries the updated child count and names of spawned sessions.
type spawnResult struct {
	ChildCount int
	ChildNames []string
}

func processSpawnSignals(
	cfg Config,
	iteration int,
	spawnSignals []signals.SpawnSignal,
	spawnedChildren int,
) (spawnResult, error) {
	res := spawnResult{ChildCount: spawnedChildren}
	if len(spawnSignals) == 0 {
		return res, nil
	}

	maxChildren := cfg.SpawnMaxChildren
	if maxChildren <= 0 {
		maxChildren = defaultMaxChildSessions
	}

	maxDepth := cfg.SpawnMaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxSpawnDepth
	}

	parentTarget, err := spawnParentTarget(cfg)
	if err != nil {
		return res, err
	}

	cursorJSON := marshalCursorJSON(iteration, cfg.Provider.Name(), "", 0)

	for idx, spawnSignal := range spawnSignals {
		sigID := SignalID(iteration, "spawn", idx)

		failure := func(reason error) error {
			if cfg.Store != nil {
				data := map[string]any{
					"signal_id":     sigID,
					"iteration":     iteration,
					"signal_index":  idx,
					"run":           spawnSignal.Run,
					"child_session": spawnSignal.Session,
					"project_root":  strings.TrimSpace(spawnSignal.ProjectRoot),
					"error":         reason.Error(),
				}
				dataJSON, _ := json.Marshal(data)
				_ = cfg.Store.AppendEvent(context.Background(), cfg.Session,
					store.TypeSignalSpawnFailed, cursorJSON, string(dataJSON))
			}
			return nil
		}

		if cfg.SpawnDepth >= maxDepth {
			if err := failure(fmt.Errorf("max_spawn_depth exceeded (%d)", maxDepth)); err != nil {
				return res, err
			}
			continue
		}

		if res.ChildCount >= maxChildren {
			if err := failure(fmt.Errorf("max_child_sessions reached (%d)", maxChildren)); err != nil {
				return res, err
			}
			continue
		}

		if cfg.Launcher == nil {
			if err := failure(session.ErrLauncherRequired); err != nil {
				return res, err
			}
			continue
		}

		childProjectRoot, rootErr := runtarget.ResolveSpawnRoot(parentTarget.ProjectRoot, spawnSignal.ProjectRoot)
		if rootErr != nil {
			if appendErr := failure(fmt.Errorf("resolve spawn project_root: %w", rootErr)); appendErr != nil {
				return res, appendErr
			}
			continue
		}
		targetSource := runtarget.SourceSpawnInherit
		if strings.TrimSpace(spawnSignal.ProjectRoot) != "" {
			targetSource = runtarget.SourceSpawnOverride
		}
		childTarget, targetErr := runtarget.Resolve(childProjectRoot, targetSource)
		if targetErr != nil {
			if appendErr := failure(fmt.Errorf("resolve child run target: %w", targetErr)); appendErr != nil {
				return res, appendErr
			}
			continue
		}

		parsed, err := spec.ParseWithOptions(spawnSignal.Run, spec.ParseOptions{
			StageResolveOpts: stage.ResolveOptions{ProjectRoot: childTarget.ProjectRoot},
		})
		if err != nil {
			if appendErr := failure(fmt.Errorf("parse run spec: %w", err)); appendErr != nil {
				return res, appendErr
			}
			continue
		}

		parsed, err = applySpawnCountOverride(parsed, spawnSignal.N)
		if err != nil {
			if appendErr := failure(err); appendErr != nil {
				return res, appendErr
			}
			continue
		}
		childStage := spawnSignal.Run
		if stageSpec, ok := parsed.(spec.StageSpec); ok {
			childStage = stageSpec.Name
		}

		// Two-phase: emit dispatching before the side effect.
		if cfg.Store != nil {
			dispData := map[string]any{
				"signal_id":   sigID,
				"signal_type": "spawn",
				"iteration":   iteration,
			}
			dataJSON, _ := json.Marshal(dispData)
			_ = cfg.Store.AppendEvent(context.Background(), cfg.Session,
				store.TypeSignalDispatching, cursorJSON, string(dataJSON))
		}

		childSession, err := session.Start(parsed, spawnSignal.Session, session.StartOpts{
			ProjectRoot:   childTarget.ProjectRoot,
			RunTarget:     childTarget,
			TargetSource:  childTarget.Source,
			Provider:      cfg.Provider.Name(),
			Model:         cfg.Model,
			Context:       spawnSignal.Context,
			ParentSession: cfg.Session,
			SpawnDepth:    cfg.SpawnDepth + 1,
			Executable:    cfg.ExecutablePath,
			Launcher:      cfg.Launcher,
			LauncherOpts: session.StartOptions{
				WorkDir: childTarget.ProjectRoot,
			},
		})
		if err != nil {
			if appendErr := failure(fmt.Errorf("start child session: %w", err)); appendErr != nil {
				return res, appendErr
			}
			continue
		}

		res.ChildCount++
		res.ChildNames = append(res.ChildNames, childSession.Name)

		if cfg.Store != nil {
			spawnData := map[string]any{
				"signal_id":     sigID,
				"iteration":     iteration,
				"signal_index":  idx,
				"run":           spawnSignal.Run,
				"child_stage":   childStage,
				"child_session": childSession.Name,
				"child_run_dir": childSession.RunDir,
				"project_root":  childTarget.ProjectRoot,
				"repo_root":     childTarget.RepoRoot,
				"config_root":   childTarget.ConfigRoot,
				"project_key":   childTarget.ProjectKey,
				"target_source": childTarget.Source,
				"pid":           childSession.Handle.PID,
				"backend":       childSession.Handle.Backend,
			}
			dataJSON, _ := json.Marshal(spawnData)
			_ = cfg.Store.AppendEvent(context.Background(), cfg.Session,
				store.TypeSignalSpawn, cursorJSON, string(dataJSON))
		}

		if err := dispatchSignalHandlers(dispatchSignalInput{
			Store:         cfg.Store,
			Session:       cfg.Session,
			Stage:         cfg.StageName,
			Iteration:     iteration,
			SignalID:      sigID,
			SignalType:    "spawn",
			Handlers:      cfg.SpawnHandlers,
			Timeout:       cfg.SignalHandlerTimeout,
			Output:        cfg.SignalOutput,
			ChildSession:  childSession.Name,
			ChildStage:    childStage,
			CallbackURL:   cfg.CallbackURL,
			CallbackToken: cfg.CallbackToken,
		}); err != nil {
			return res, fmt.Errorf("dispatch spawn handlers: %w", err)
		}
	}

	return res, nil
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

func spawnParentTarget(cfg Config) (runtarget.Target, error) {
	if strings.TrimSpace(cfg.RunTarget.ProjectRoot) != "" {
		return runtarget.NormalizeWithDefaults(cfg.RunTarget, cfg.RunTarget.Source)
	}
	return runtarget.Resolve(cfg.WorkDir, runtarget.SourceSpawnInherit)
}
