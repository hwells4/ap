# AGENTS.md — ap: Agent Pipeline Orchestrator

Go CLI that runs autonomous AI agent pipelines. Rewrites the Bash [agent-pipelines](https://github.com/hwells4/agent-pipelines) system. Binary: `ap`. Module: `github.com/hwells4/ap`.

## What ap Does

Runs multi-iteration agent workflows in tmux sessions. Each iteration spawns a fresh agent (Claude, Codex, Gemini, or any CLI tool) that reads accumulated progress — fresh context every time, no degradation. Two-agent consensus prevents premature stopping. Everything is a pipeline; a "loop" is a single-stage pipeline.

```
ap run ralph my-session                    # stage name → 1-stage pipeline
ap run ralph:25 my-session                 # with iteration count
ap run "improve-plan:5 -> refine-tasks:5"  # chain → multi-stage
ap run refine.yaml my-session              # YAML → full pipeline (parallel blocks, custom routing)
ap run ./custom.md my-session              # prompt file → 1-stage pipeline
```

Seven commands: `run`, `list`, `status`, `resume`, `kill`, `logs`, `clean`. Eight flags: `-n`, `--provider`, `-m`, `-i`, `-c`, `-f`, `--fg`, `--json`. Exit codes: 0=success, 2=bad args, 3=not found, 4=exists, 5=locked, 10=provider error, 11=timeout, 20=paused. `kill`/`clean` are idempotent (exit 0 even if target doesn't exist).

## Architecture

```
CLI (cmd/ap)  →  Spec Parser (internal/spec)  →  Runner (internal/runner)  →  Provider (pkg/provider)
                                                      ↓
                                              Signals (internal/signals) → inject | spawn | escalate
```

**Runner loop** (`internal/runner`): For each iteration: generate context.json → resolve prompt template → execute provider → parse status.json → process signals → check termination → emit events. Runner owns termination precedence: (1) escalate signal pauses immediately, (2) provider failure → retry policy, (3) agent `error` → stop, (4) agent `stop` → stop early, (5) judgment consensus → stop, (6) iteration count reached → stop.

**Background by default.** `ap run` launches via Launcher interface (TmuxLauncher or ProcessLauncher fallback). Two-phase startup confirmation per launcher type (tmux `wait-for` channels / `os.Pipe()` FD inheritance).

## Package Map

| Package | Status | Purpose |
|---------|--------|---------|
| `internal/exec` | Done | Process execution, signal cascade (SIGTERM→SIGKILL), bounded I/O, process groups |
| `internal/events` | Done | Append-only events.jsonl with flock |
| `internal/state` | Done | Session lifecycle + crash recovery, `ResumeFrom()` |
| `internal/context` | Done | context.json generation per iteration |
| `internal/resolve` | Done | `${VAR}` template substitution (single pass, left-to-right) |
| `internal/result` | Done | Agent output normalization from status.json |
| `internal/termination` | Done | Fixed iteration strategy |
| `internal/validate` | Done | Security validation |
| `internal/stage` | Done | Stage definition resolution (project → plugin → builtins) |
| `internal/engine` | Done | Provider registry (thin wrapper) |
| `pkg/provider` | Done | SDK: Provider interface, ExecuteRequest/ExecuteResult types |
| `pkg/provider/claude` | Done | Claude CLI provider |
| `internal/runner` | M0a | **Core**: iteration loop orchestrator |
| `internal/spec` | M0a | Unified spec parser → typed AST (StageSpec/ChainSpec/FileSpec) |
| `internal/mock` | Pre-M0 | MockProvider: deterministic canned responses for tests |
| `internal/testutil` | Pre-M0 | FakeProviderBin, Clock, IDGen, TempSession, BDFake |
| `internal/fsutil` | Pre-M0 | Shared file existence helper |
| `internal/signals` | M0c | Signal parsing, two-phase dispatch lifecycle, handler chain |
| `internal/session` | M0b | Session launcher (delegates to TmuxLauncher/ProcessLauncher) |
| `internal/judge` | M1 | Judgment provider invocation for two-agent consensus |
| `internal/compile` | M3 | YAML pipeline compiler |
| `internal/parallel` | M4 | Parallel provider execution |
| `internal/messages` | M4 | Live message bus for `ap watch` |

## Key Contracts (Frozen)

**Provider interface** — `pkg/provider.Provider`: `Execute(ctx, ExecuteRequest) (*ExecuteResult, error)`. Request has Prompt, WorkingDir, Timeout, Model, EnvVars, InputFiles. Result has ExitCode, Stdout, Stderr, Duration, StatusJSON. Runner sets env: `AP_AGENT=1`, `AP_SESSION`, `AP_STAGE`, `AP_ITERATION`. Decision comes from status.json ONLY — never stdout/stderr.

**Template variables** — `${CTX}`, `${PROGRESS}`, `${STATUS}`, `${ITERATION}`, `${SESSION_NAME}`, `${CONTEXT}`, `${OUTPUT}`. Substituted in prompt.md only. Undefined vars left as literal `${VAR}`. Single pass, no recursion.

**Events** — Append-only events.jsonl. Required fields: `type`, `timestamp` (ISO 8601), `session`. Types: `session.started/completed`, `iteration.started/completed/failed`, `signal.dispatching/dispatched/spawn/escalate`. Strict ordering guarantees.

**Signals** — Agents emit via `agent_signals` in status.json. `inject`: prepend text to next `${CONTEXT}`, consumed once. `spawn`: launch child session (same spec syntax as CLI), parent continues. `escalate`: always pauses session, dispatch to handler chain (stdout → webhook → exec). Processing order: inject → spawn → escalate (escalate always last because it pauses). Two-phase lifecycle: `signal.dispatching` event before dispatch, result event after. Deterministic IDs: `sig-{iter}-{type}-{idx}`.

**Stage-to-stage data** — Output = last iteration's `output.md`. Injected via `context.json` `inputs` field. Chain default: `from: previous`, `select: latest`. Parallel: keyed by provider name in `from_parallel`.

## Data Layout

```
.ap/runs/{session}/run_request.json    # durable request record (spec, provider, model, etc.)
.ap/runs/{session}/state.json          # pre-computed snapshot (O(1) status reads)
.ap/runs/{session}/events.jsonl        # append-only event log
.ap/runs/{session}/stage-NN-{name}/iterations/{NNN}/   # per-iteration data
.ap/locks/{session}.lock               # session locks
~/.config/ap/config.yaml               # global config (signal handlers, limits, defaults)
```

## Development Rules

**Build/test**: `go build ./...` then `go test ./...` then `go vet ./...` — all three must pass.
**TDD**: Test first, implement second, refactor third. No exceptions. MockProvider is day-1 infra.
**Test layers**: 60% unit, 30% integration (process boundaries, file I/O, crash recovery), 10% E2E (real binary). Integration: `TestIntegration_*`. E2E: `TestE2E_*` gated behind `AP_E2E=1`. Race detector: `go test -race` for all concurrent code.
**Dependencies**: Near-zero. Only `gopkg.in/yaml.v3` beyond stdlib. Do not add deps without approval.
**Process execution**: Always `internal/exec.Run()`. Never raw `os/exec`.
**Errors**: Wrap with `fmt.Errorf("package: context: %w", err)`.

## Milestones

```
Pre-M0 → M0a → M0b → M0c → M1 → M2 → M3 → M4 → M5
```

| MS | Deliverable |
|----|-------------|
| Pre-M0 | Structural cleanup: unify Provider, add yaml.v3, rename cmd/ap, mock, fsutil, test infra |
| M0a | Minimal `ap run ralph my-session` — single-stage loop that works end-to-end |
| M0b | Session management: `ap logs`, `ap clean`, crash recovery, lifecycle telemetry, fuzzy matching |
| M0c | Agent signals: inject/spawn/escalate, two-phase lifecycle, spawn limits, resume idempotency |
| M1 | Judgment termination: two-agent consensus strategy |
| M2 | Multi-provider (Codex), config.yaml loader, signal handlers (webhook/exec), auto-retry backoff |
| M3 | Multi-stage pipelines: YAML compiler, chain parser, stage-to-stage data flow |
| M4 | Parallel blocks: parallel provider execution, message bus, manifest aggregation |
| M5 | Watch command, race termination |

Each milestone produces something runnable. Do not skip ahead. Use `bd ready` to find unblocked tasks.

## Beads (Task Tracking)

```bash
bd ready                    # unblocked tasks
bd list --label pre-m0      # filter by milestone
bd show <id>                # full details + acceptance criteria
bd claim <id>               # claim before starting
bd done <id>                # mark complete when proof command passes
```

## Stage Library

`stages/` contains stage definitions (stage.yaml + prompt.md). `pipelines/` contains YAML pipeline definitions. Built-in stages are embedded via `go:embed`. User stages in `.ap/stages/` override builtins. `ap list` scans and prints available stages.

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
