# AGENTS.md — ap: Agent Pipeline Orchestrator

Go CLI that runs autonomous AI agent pipelines. Rewrites the Bash [agent-pipelines](https://github.com/hwells4/agent-pipelines) system. Binary: `ap`. Module: `github.com/hwells4/ap`.

## What ap Does

Runs multi-iteration agent workflows in tmux sessions. Each iteration spawns a fresh agent (Claude, Codex, Gemini, or any CLI tool) that reads accumulated progress — fresh context every time, no degradation. Two-agent consensus prevents premature stopping. Everything is a pipeline; a "loop" is a single-stage pipeline.

```
ap run ralph my-session                              # stage name → 1-stage pipeline
ap run ralph:25 my-session                           # with iteration count
ap run "improve-plan:5 -> refine-tasks:5" my-session # chain → multi-stage
ap run refine.yaml my-session                        # YAML → full pipeline
ap run ./custom.md my-session                        # prompt file → 1-stage pipeline
```

## CLI Surface

Seven commands: `run`, `list`, `status`, `resume`, `kill`, `logs`, `clean`. Flags: `-n COUNT`, `--provider NAME`, `-m MODEL`, `-i INPUT` (repeatable), `-c CONTEXT`, `-f` (force), `--fg` (foreground), `--json`, `--explain-spec` (dry-run).

**Exit codes**: 0=success, 2=bad args, 3=not found, 4=exists, 5=locked, 10=provider error, 11=timeout, 20=paused. `kill`/`clean` are idempotent (exit 0 even if target gone).

**Spec resolution** (`internal/spec`): Contains `->` → ChainSpec. Ends `.yaml/.yml` → FileSpec(yaml). Ends `.md` or starts `./`/`/` → FileSpec(prompt). Contains `:` → StageSpec(name, count). Otherwise → StageSpec(name). All produce the same Pipeline object internally.

**Robot mode**: `--json` flag, non-TTY stdout, or `AP_OUTPUT=json` → JSON mode. Every command supports it. Structured errors: `{code, message, detail, syntax, suggestions[], available_*}`. Error codes are SCREAMING_SNAKE. Success responses include `corrections[]` array (empty if no fuzzy match).

**Fuzzy matching** (M0b): Command synonyms (`start`→`run`, `ls`→`list`, `stop`→`kill`, etc.), Levenshtein on commands/stages (distance 2 for 5+ chars, 1 for ≤4), flag aliases (`--iterations`→`-n`, `--provider anthropic`→`--provider claude`), argument order recovery, spec syntax recovery (`ralph 25`→`ralph:25`). Safety: NO fuzzy/swap for `kill`/`clean` — exact synonyms only. All corrections returned in `corrections[]`.

**Background by default.** `ap run` launches via Launcher (TmuxLauncher default, ProcessLauncher fallback when no tmux). Two-phase startup: TmuxLauncher uses `tmux wait-for` channels, ProcessLauncher uses `os.Pipe()` with `cmd.ExtraFiles`. `--fg` for foreground (CI/testing).

## Architecture

```
CLI (cmd/ap) → Spec Parser (internal/spec) → Runner (internal/runner) → Provider (pkg/provider)
                                                    ↓
                                            Signals (internal/signals) → inject | spawn | escalate
```

**Runner loop** (`internal/runner`): generate context.json → resolve prompt template → execute provider → parse status.json → process signals → check termination → update state → emit events.

**Termination precedence** (runner-owned, centrally enforced):
1. `escalate` signal → pause immediately
2. Provider failure → retry policy decides
3. Agent decision `error` → stop stage
4. Agent decision `stop` → stop early
5. Judgment consensus → stop
6. Fixed iteration count → stop

**Launcher interface**: `Start(session, runnerCmd, opts) (SessionHandle, error)` + `Kill(session) error`. Internal runner subcommand: `ap _run --session <name> --request <path>` — hidden, deterministic, no fuzzy parsing. All Launchers invoke `ap _run`, never `ap run`.

## Package Map

| Package | Status | Purpose |
|---------|--------|---------|
| `internal/exec` | Done | Process execution, SIGTERM→SIGKILL cascade, bounded I/O (1MB), process groups |
| `internal/events` | Done | Append-only events.jsonl with flock |
| `internal/state` | Done | Session lifecycle + crash recovery, `ResumeFrom()` |
| `internal/context` | Done | context.json generation per iteration |
| `internal/resolve` | Done | `${VAR}` template substitution (single pass, left-to-right) |
| `internal/result` | Done | Agent output normalization from status.json |
| `internal/termination` | Done | Fixed iteration strategy |
| `internal/validate` | Done | Security validation |
| `internal/stage` | Done | Stage resolution (project → plugin → builtins) |
| `internal/engine` | Done | Provider registry (thin wrapper) |
| `pkg/provider` | Done | SDK: Provider interface, ExecuteRequest/ExecuteResult |
| `pkg/provider/claude` | Done | Claude CLI provider |
| `internal/runner` | M0a | **Core**: iteration loop orchestrator |
| `internal/spec` | M0a | Unified spec parser → typed AST |
| `internal/output` | M0a | Output mode detection, JSON formatting, exit codes, structured errors |
| `internal/mock` | Pre-M0 | MockProvider: deterministic canned responses for tests |
| `internal/testutil` | Pre-M0 | FakeProviderBin, Clock, IDGen, TempSession, BDFake |
| `internal/fsutil` | Pre-M0 | Shared file existence helper |
| `internal/signals` | M0c | Signal parsing, two-phase dispatch, handler chain |
| `internal/session` | M0b | Session launcher (TmuxLauncher/ProcessLauncher) |
| `internal/lock` | M0b | Flock-based session locking with stale PID detection |
| `internal/fuzzy` | M0b | Command/stage fuzzy matching, flag normalization |
| `internal/judge` | M1 | Judgment provider invocation (two-agent consensus) |
| `internal/compile` | M3 | YAML pipeline compiler |
| `internal/parallel` | M4 | Parallel provider execution |
| `internal/messages` | M4 | Live message bus for `ap watch` |

## Key Contracts (Frozen)

### Provider Interface (`pkg/provider`)

```go
Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResult, error)

ExecuteRequest { Prompt, WorkingDir, Timeout, Model string, EnvVars map[string]string, InputFiles []string }
ExecuteResult  { ExitCode int, Stdout/Stderr string, Duration, StatusJSON *AgentStatus }
```

Runner sets env: `AP_AGENT=1`, `AP_SESSION`, `AP_STAGE`, `AP_ITERATION`. Decision comes from status.json ONLY — never stdout/stderr. Provider flags: Claude uses `--dangerously-skip-permissions -p`; Codex uses `--dangerously-bypass-approvals-and-sandbox --ephemeral -`. Both receive prompt via stdin. Both executed via `internal/exec.Run()`.

### status.json (Agent Output)

```json
{
  "decision": "continue|stop|error",
  "reason": "why",
  "summary": "what was done",
  "work": { "items_completed": [...], "files_touched": [...] },
  "errors": [],
  "agent_signals": {
    "inject": "text for next iteration's ${CONTEXT}",
    "spawn": [{"run": "stage-or-chain", "session": "name", "context": "...", "n": 5}],
    "escalate": {"type": "human", "reason": "...", "options": ["A", "B"]}
  }
}
```

### Template Variables

`${CTX}` (context.json path), `${PROGRESS}` (progress file), `${STATUS}` (status.json write path), `${ITERATION}` (1-based), `${SESSION_NAME}`, `${CONTEXT}` (injected text), `${OUTPUT}` (output file path). Substituted in prompt.md only. Undefined vars left as literal `${VAR}`. Single pass, no recursion, no escape syntax.

### Events (events.jsonl)

Append-only. Required fields: `type`, `timestamp` (ISO 8601), `session`. Types: `session.started/completed`, `iteration.started/completed/failed`, `signal.inject`, `signal.dispatching`, `signal.spawn/spawn.failed`, `signal.escalate/escalate.failed`, `signal.handler.error`. Strict ordering. Write ordering for crash consistency: append event → fsync → write state.json atomically (temp+rename) → fsync.

### Signals Protocol

Agents emit via `agent_signals` in status.json. Processing order: inject → spawn → escalate (escalate always last — it pauses).

- **inject**: Prepend text to next `${CONTEXT}`, consumed once, cleared after.
- **spawn**: Launch child session via `session.Start()` (same spec syntax as CLI). Parent continues. Children are independent processes (survive parent crashes). Limits: `max_child_sessions`=10, `max_spawn_depth`=3.
- **escalate**: Always pauses session (overrides `decision`). Dispatch to handler chain: stdout (always) → webhook (if configured) → exec (if configured). Ephemeral callback listener on `127.0.0.1:0` for automated response; falls back to `ap resume` for manual.

**Two-phase lifecycle**: `signal.dispatching` event (with deterministic ID `sig-{iter}-{type}-{idx}`) → execute side effect → type-specific result event. On resume: dispatching+result → skip; dispatching+no result → re-dispatch.

### Stage-to-Stage Data (M3)

Output = last iteration's `output.md`. Injected via `context.json` `inputs` field. Chain default: `from: previous`, `select: latest`. Parallel (M4): keyed by provider name in `from_parallel`.

## Data Layout

```
.ap/runs/{session}/run_request.json                     # durable request (spec, provider, model)
.ap/runs/{session}/state.json                           # pre-computed snapshot (O(1) status reads)
.ap/runs/{session}/events.jsonl                         # append-only event log
.ap/runs/{session}/stage-NN-{name}/iterations/{NNN}/    # per-iteration data
.ap/locks/{session}.lock                                # flock-based session locks
~/.config/ap/config.yaml                                # global config (signal handlers, limits)
```

**state.json** is bounded: `recent_iterations` max 5, `files_touched` max 50, `signals` max 20, `child_sessions` max 10. Oldest evicted. `last_event_offset` for incremental replay. `ap status --json` returns this object directly.

## Development Rules

**Build/test**: `go build ./... && go test ./... && go vet ./...` — all three must pass.

**TDD**: Test first, implement second, refactor third. No exceptions. MockProvider is day-1 infra.

**Test layers**: 60% unit (table-driven, no I/O), 30% integration (real filesystem, process boundaries, crash recovery), 10% E2E (real binary). Naming: `TestFoo` (unit), `TestIntegration_*`, `TestE2E_*` (gated `AP_E2E=1`). Race detector: `go test -race` for all concurrent code.

**Dependencies**: Near-zero. Only `gopkg.in/yaml.v3` beyond stdlib. Do not add deps without approval.

**Code rules**: Process execution always via `internal/exec.Run()`. Errors: `fmt.Errorf("package: context: %w", err)`. Template syntax: `${VAR}` only, no Go templates. State transitions via `internal/state`. Events via `internal/events`.

## Milestones

```
Pre-M0 → M0a → M0b → M0c → M1 → M2 → M3 → M4 → M5
```

| MS | Days | Deliverable |
|----|------|-------------|
| Pre-M0 | 1 | Unify Provider, add yaml.v3, rename cmd/ap, mock, fsutil, test infra |
| M0a | 2 | Minimal `ap run` foreground loop — runner, spec parser, output layer, structured errors |
| M0b | 3 | Background launch, session mgmt (status/logs/kill/clean/resume), stage lookup, lock, fuzzy matching |
| M0c | 2 | Agent signals: inject/spawn/escalate, two-phase lifecycle, spawn limits, resume idempotency |
| M1 | 1 | Judgment termination: two-agent consensus, min iterations, judge failure fallback |
| M2 | 2 | Codex provider, config.yaml, signal handlers (webhook/exec), auto-retry backoff, `--on-escalate` |
| M3 | 2 | Multi-stage: YAML compiler, chain parser, stage-to-stage input passing |
| M4 | 1 | Parallel blocks: concurrent providers, message bus, manifest aggregation |
| M5 | 1 | Watch command, race termination |

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

`stages/` has 34 stage definitions (stage.yaml + prompt.md). `pipelines/` has YAML pipeline definitions. Built-in stages embedded via `go:embed`. User stages in `.ap/stages/` override builtins. `ap list` scans and prints.

## What's Cut

No `library.yaml` (stage.yaml suffices). No `{{.param}}` Go templates (use `${VAR}`). No `--set/--inline/--termination/--command` flags (use stage.yaml + env vars). No `ap compile/validate` commands (validation in `ap run`). No `checkpoint`/`budget` signals in v1 (reserved, logged as warnings). No hooks (`internal/hooks`) until needed.

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
