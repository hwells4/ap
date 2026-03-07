# AGENTS.md — `ap` CLI Contract for AI Agents

`ap` is a machine-first CLI for running autonomous agent pipelines in tmux/process sessions.
Binary: `ap`. Module: `github.com/hwells4/ap`.

## Quick Reference
| Want | Command |
| --- | --- |
| Start a stage | `ap run <stage> <session>` |
| Start with fixed iterations | `ap run <stage>:<n> <session>` |
| Explain spec only | `ap run <spec> <session> --explain-spec --json` |
| List available stages | `ap list [--json]` |
| Check session snapshot | `ap status <session> [--project-root DIR] [--json]` |
| Resume paused/failed session | `ap resume <session> [--context "..."] [--project-root DIR] [--json]` |
| Terminate a session | `ap kill <session> [--project-root DIR] [--json]` |
| Read session events | `ap logs <session> [-f] [--project-root DIR] [--json]` |
| Watch sessions live | `ap watch [--json]` |
| Query sessions | `ap query sessions [--status STATUS] [--json]` |
| Query iterations | `ap query iterations --session NAME [--stage NAME] [--json]` |
| Query events | `ap query events --session NAME [--type TYPE] [--after SEQ] [--json]` |
| Clean run artifacts | `ap clean <session>|--all [--force] [--project-root DIR] [--json]` |

## Storage Model
- All session state lives in a single **SQLite database** (`.ap/ap.db`, WAL mode).
- Tables: `sessions`, `iterations`, `outputs`, `events`, `locks`, `session_children`, `schema_version`.
- The `events` table is an append-only audit log with monotonic `seq` per session.
- The `iterations` table stores per-iteration decision, summary, exit code, and signals.
- The `outputs` table stores stdout/stderr/context_json paired 1:1 with iterations.
- Decision authority is the SQLite store (not stdout/stderr or any JSON file on disk).
- A **machine-wide control plane** (`~/.local/state/ap/control.db`) indexes sessions across all projects. This enables cross-project session lookup via `--project-root` or automatic resolution when a session name is unique.
- **Session state machine** (enforced at store level):
  - `running` → `paused`, `completed`, `failed`, `aborted`
  - `paused` → `running`, `aborted`
  - `failed` → `running`, `aborted`
  - `completed` and `aborted` are **terminal** (no further transitions).

## Context File Contract

Each iteration receives a `context.json` at `${CTX}`. This is the primary interface between runner and agent.

```json
{
  "session": "my-session",
  "pipeline": "pipeline-name",
  "stage": {
    "id": "improve-plan",
    "index": 0,
    "template": "improve-plan"
  },
  "iteration": 3,
  "paths": {
    "session_dir": ".ap/runs/my-session",
    "stage_dir": "stage-00-improve-plan",
    "progress": ".ap/runs/my-session/progress.md",
    "history": ".ap/runs/my-session/stage-00-improve-plan/iterations/003/history.md",
    "output": ".ap/runs/my-session/stage-00-improve-plan/iterations/003/output.md",
    "output_path": "resolved/custom/path.md",
    "messages": ".ap/runs/my-session/messages.jsonl"
  },
  "inputs": {
    "from_stage": {"plan-stage": ["/path/to/output.md"]},
    "from_previous_iterations": ["/path/iteration-001/output.md"],
    "from_initial": ["/path/input1.md"],
    "from_parallel": [{"stage": "...", "block": "..."}]
  },
  "limits": {
    "max_iterations": 25,
    "remaining_seconds": 3600
  },
  "commands": {}
}
```

## Template Variables

All `${VAR}` placeholders resolved in prompts and `output_path`:

| Variable | Description |
|----------|-------------|
| `${CTX}` | Full path to `context.json` |
| `${SESSION}` | Session name |
| `${SESSION_NAME}` | Alias for `${SESSION}` |
| `${ITERATION}` | Current iteration number (1-based) |
| `${INDEX}` | Zero-based iteration index (`ITERATION - 1`) |
| `${PROGRESS}` | Path to `progress.md` |
| `${PROGRESS_FILE}` | Alias for `${PROGRESS}` |
| `${HISTORY}` | Path to `history.md` |
| `${OUTPUT}` | Path to iteration `output.md` |
| `${OUTPUT_PATH}` | Resolved `output_path` from stage config |
| `${CONTEXT}` | Injected context (from `signal.inject` or `--context`) |
| `${MESSAGES}` | Path to `messages.jsonl` |
| `${PERSPECTIVE}` | Optional perspective field (parallel blocks) |

## Iteration Lifecycle

Each iteration: generate context → resolve prompt → execute provider → extract result → check termination → update store → emit events.

### Result Extraction

Agents emit decisions via an `ap-result` fenced block in stdout:

````
```ap-result
{
  "decision": "continue",
  "summary": "Implemented feature X",
  "signals": {
    "inject": "context for next iteration",
    "escalate": {"type": "human", "reason": "need approval"},
    "spawn": [{"run": "review:3", "session": "child-review"}]
  }
}
```
````

**Extraction strategy** (first match wins):
1. Parse last `` ```ap-result `` fenced block as JSON
2. Non-zero exit code → `"error"`
3. Default → `"continue"` with last 200 chars as summary

**Valid decisions**: `continue`, `stop`, `error`

## Agent Signals

Signals are directives inside `ap-result` that trigger runner-side actions.

| Signal | Effect |
|--------|--------|
| `inject` | String injected as `${CONTEXT}` in the next iteration |
| `escalate` | Dispatches to escalation handler chain; pauses session |
| `spawn` | Launches child sessions (subject to `max_child_sessions` / `max_spawn_depth`) |
| `warnings` | Array of strings logged as warnings (informational only) |

**Escalate payload**:
```json
{"type": "human", "reason": "Need design review", "options": ["approve", "reject"]}
```

**Spawn payload**:
```json
[{"run": "stage:5", "session": "child-name", "project_root": "...", "n": 10}]
```

## Termination Strategies

| Type | Config | Description |
|------|--------|-------------|
| `fixed` | `iterations: N` | Stop after N iterations. Default: 1. Immediate stop on `"stop"` or `"error"` decision. |
| `judgment` | `consensus: 2, min_iterations: 3` | Judge model evaluates after each iteration. Needs N consecutive `"stop"` verdicts. Falls back to fixed after 3 judge failures. |
| `race` | `agents: 2, accept: first` | N concurrent providers run in parallel. First successful result wins. |

## Retry

Per-iteration retry on provider failure:

| Field | Default | Description |
|-------|---------|-------------|
| `max_attempts` | `1` (no retry) | Total attempts per iteration |
| `backoff` | `5s` | Initial backoff; doubles per attempt |
| `on_exhausted` | `"abort"` | `"abort"` fails the session, `"pause"` pauses for investigation |

## Signal Handlers

Handler chain for `escalate` and `spawn` events. Configured in `~/.config/ap/config.yaml`:

| Type | Required Fields | Description |
|------|----------------|-------------|
| `stdout` | — | Print JSON payload to stdout (default fallback) |
| `webhook` | `url`, optional `headers` | HTTP POST with JSON payload. Includes `callback_url`/`callback_token` when callback listener is active. |
| `exec` | `argv` | Execute subprocess. `argv[0]` is not expanded; args support `${SESSION}`, `${STAGE}`, `${ITERATION}`, `${REASON}`, `${CHILD_SESSION}`, `${TYPE}`. |

**Callback listener**: When configured, an ephemeral HTTP server listens for `POST /resume` responses. The `callback_url` and `callback_token` are included in webhook payloads for human-in-the-loop responses. Non-localhost binds auto-generate bearer tokens.

## Lifecycle Hooks

Deterministic shell commands executed by the runner at key lifecycle points. Non-fatal: failures emit `hook.failed` events but do not stop the session.

| Hook | When | Example Use Case |
|------|------|-----------------|
| `pre_session` | Once, before first iteration | `git checkout -b ap/${SESSION}` |
| `pre_iteration` | Before each iteration starts | Pull latest, run linter gate |
| `pre_stage` | Before a pipeline stage begins | Initialize stage resources |
| `post_iteration` | After each completed iteration | `git add -A && git commit -m "$AP_SUMMARY"` |
| `post_stage` | After a pipeline stage completes | Stage-level commit/tag |
| `post_session` | After session completes successfully | `git push -u origin HEAD && gh pr create --fill` |
| `on_failure` | When session fails | Cleanup, notification |

**Precedence** (most-specific wins, no merging):
1. Stage hooks (`stage.yaml` `hooks:` field) — single-stage runs only
2. Pipeline hooks (`pipeline.yaml` top-level `hooks:` field)
3. Global hooks (`~/.config/ap/config.yaml` `hooks:` field)

**Execution model**:
- Shell: `sh -c <command>` (POSIX)
- Working directory: project root
- Environment: inherits parent process env plus `AP_SESSION`, `AP_STAGE`, `AP_ITERATION`, `AP_STATUS`, `AP_SUMMARY`
- Variable substitution: `${SESSION}`, `${STAGE}`, `${ITERATION}`, `${STATUS}`, `${SUMMARY}` in command strings
- `${SUMMARY}` / `$AP_SUMMARY`: the agent's iteration summary (from ap-result). Available to all hooks (accumulated state). Use `$AP_SUMMARY` (env var) for commit messages — it's shell-safe.
- Timeout: 60 seconds default (configurable via `hooks.timeout` in config.yaml)

**Configuration examples**:

Global (`~/.config/ap/config.yaml`):
```yaml
hooks:
  pre_session: "git checkout -b ap/${SESSION} 2>/dev/null || git checkout ap/${SESSION}"
  post_iteration: 'git add -A && git diff --cached --quiet || git commit -m "$AP_SUMMARY"'
  post_session: "git push -u origin HEAD"
  timeout: 30s
```

Stage (`stages/ralph/stage.yaml`):
```yaml
name: ralph
hooks:
  post_iteration: "git add -A && git diff --cached --quiet || git commit -m 'ralph: iter ${ITERATION}'"
```

Pipeline (`pipelines/build-and-deploy.yaml`):
```yaml
name: build-and-deploy
hooks:
  pre_session: "git checkout -b deploy/${SESSION}"
  post_session: "git push -u origin HEAD && gh pr create --fill"
nodes:
  - id: implement
    stage: ralph
    runs: 5
```

## Session History & Progress

| File | Location | Written By | Purpose |
|------|----------|-----------|---------|
| `progress.md` | `.ap/runs/{session}/progress.md` | Agent | Session-scoped working notes. Survives stage transitions. Stage boundary markers appended at transitions. |
| `history.md` | `.ap/runs/{session}/stage-XX-{id}/iterations/NNN/history.md` | Runner | Deterministic iteration summary rebuilt from SQLite before each iteration. Read-only for agents. |

**History format**:
```markdown
# Session History
## Stage: stage-name
- **Iteration 1** [continue]: Summary of iteration 1
- **Iteration 2** [stop]: Summary of iteration 2
```

## Work Manifest

Each iteration captures a git diff telemetry snapshot stored in iteration outputs:

```json
{
  "git": {
    "pre_head": "abc123",
    "post_head": "def456",
    "diff_stat": "3 files changed, 42 insertions(+), 7 deletions(-)",
    "files_changed": ["path/to/file1.go", "path/to/file2.go"]
  }
}
```

## Event Types

| Event | Description |
|-------|-------------|
| `session.started` | Session started |
| `session.completed` | Session completed |
| `node.started` | Pipeline node started |
| `node.completed` | Pipeline node completed |
| `iteration.started` | Iteration started |
| `iteration.completed` | Iteration completed |
| `iteration.failed` | Iteration failed |
| `iteration.retried` | Iteration retry attempt |
| `judge.verdict` | Judgment termination evaluation result |
| `judge.fallback` | Judge fell back to fixed termination |
| `signal.dispatching` | Signal dispatch started |
| `signal.inject` | Inject signal processed |
| `signal.escalate` | Escalation signal dispatched |
| `signal.spawn` | Child session spawned |
| `signal.spawn.failed` | Child spawn failed |
| `signal.handler.error` | Handler error (non-fatal) |
| `hook.completed` | Lifecycle hook executed successfully |
| `hook.failed` | Lifecycle hook failure (non-fatal) |
| `error` | General error |

## Run Artifact Layout

```
.ap/runs/{session}/
  run_request.json        # persisted launch config (crash recovery)
  state.json              # session state snapshot
  progress.md             # session-scoped agent notes
  messages.jsonl          # live message bus
  stage-00-{id}/
    iterations/
      001/
        context.json      # iteration context
        output.md         # agent output
        history.md        # runner-generated summary
```

## Output Contract
- JSON mode is enabled by `--json`, non-TTY stdout, or `AP_OUTPUT=json`.
- Success returns exit `0` with payload plus `corrections[]`.
- Errors return non-zero with structured JSON:
  - `error.code`, `error.message`, `error.detail`, `error.syntax`, `error.suggestions[]`.
  - Optional `error.available_*` metadata for recovery.
- Exit codes: `0` success, `2` bad args, `3` not found, `4` exists, `5` locked, `10` provider error, `11` timeout, `20` paused.

## Forgiving Syntax (M0b)
- Command synonyms: `start→run`, `ls→list`, `stop→kill`, etc.
- Typo correction: Levenshtein distance (`<=2` for long commands/stages, `<=1` for short).
- Flag alias normalization: e.g. `--iterations→-n`, `--provider anthropic→--provider claude`.
- Argument order recovery: can recover misplaced `<spec> <session>`.
- Spec recovery: can recover `stage 25` to `stage:25`.
- Chain arrow recovery: ` > ` and `,` normalized to ` -> ` (e.g. `"a:5 > b:5"` → `"a:5 -> b:5"`).
- Safety rule: `kill` and `clean` do **not** use typo-based fuzzy matching (exact synonyms only).

## Spec Types
- Stage spec: `ralph`
- Stage with count: `ralph:25`
- Chain spec: `"improve-plan:5 -> refine-tasks:5"`
- YAML pipeline file: `./pipeline.yaml`
- Prompt file: `./prompt.md`
- Chain arrow recovery: ` > ` and `,` are normalized to ` -> `.
- Stage iteration recovery: `ralph 25` → `ralph:25`.

## Common Agent Patterns
1. Validate request and spec:
   - `ap run <spec> <session> --json --explain-spec`
2. Start and track session:
   - `ap run <spec> <session> [--provider ... -m ...] --json`
3. Poll status for machine state:
   - `ap status <session> --json`
4. Read iteration/event details:
   - `ap logs <session> --json` or `ap logs <session> -f --json`
5. Query store directly:
   - `ap query sessions --status running --json` (list sessions by status)
   - `ap query iterations --session <name> --json` (all iterations for a session)
   - `ap query events --session <name> --type signal.escalate --json` (filtered events)
   - `ap query events --session <name> --after 5 --json` (events after seq 5, for polling)
6. Escalation/paused handling:
   - if paused (`exit 20`/state paused), gather context and `ap resume <session> --json`
7. Cleanup/termination:
   - `ap kill <session> --json` (idempotent)
   - `ap clean <session>|--all --json`

## Cross-Project Session Resolution
- Commands that take `<session>` support `--project-root DIR` to target a specific project.
- Without `--project-root`, resolution order: local store → control plane index → error.
- If the session name is unique across all projects, it resolves automatically.
- If ambiguous (same name in multiple projects), returns `SESSION_AMBIGUOUS` with suggestions.

## Agent Notes
- Internal launcher entrypoint is `ap _run --session <name> --request <path> [--resume]`.
- Decision authority is the SQLite store (`.ap/ap.db`), not stdout/stderr.
- Preserve and inspect `corrections[]` for deterministic machine workflows.
- `run_request.json` is also persisted to disk in `.ap/runs/<session>/` for crash recovery.
- `ap resume` re-launches the session process via the launcher (tmux/process). It also cleans orphaned iterations (stuck in "started" from a prior crash) before resuming.
- Process-level mutual exclusion uses `flock` on `.ap/locks/{session}.lock` (not the SQLite `locks` table).
