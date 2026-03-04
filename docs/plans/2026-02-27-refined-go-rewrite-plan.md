---
date: 2026-02-27
type: plan
status: draft
revision: 10
project: go-engine-rewrite
supersedes: 2026-01-15-go-engine-rewrite-prd.md
reviewed_by:
  - architecture-strategist
  - code-simplicity-reviewer
  - pattern-recognition-specialist
  - claude-opus-4.6 (multi-model synthesis round)
  - codex-gpt-5.2 (multi-model synthesis round)
  - codex-gpt-5.3 (Rev 6 review)
  - claude-opus-4.6 + codex-gpt-5.3 (elegance review)
  - claude-opus-4.6 x2 + codex-gpt-5.3 x2 (premortem review)
  - claude-opus-4.6 + codex-gpt-5.3 (robot mode design)
---

# Agent Pipelines v2: Go Rewrite Plan (Rev 10)

## What We're Building

> A CLI tool (`ap`) that runs autonomous AI agent pipelines from a single command. Everything is a pipeline. A "loop" is a single-stage pipeline. The first argument to `ap run` is what to run — a stage name, a chain, a YAML file, or a prompt file. `ap` figures out which.

```bash
ap run ralph my-session                              # stage name → 1-stage pipeline
ap run ralph:25 my-session                           # stage with iteration count
ap run "improve-plan:5 -> refine-tasks:5" my-session # ad-hoc chain → multi-stage pipeline
ap run refine.yaml my-session                        # YAML file → full pipeline definition
ap run ./custom.md my-session                        # prompt file → 1-stage pipeline
```

One command. One concept. Progressive complexity.

## Design Principles

1. **One command, one concept.** `ap run <spec> <session>` is the entire interface. The spec is a stage name, a chain, a YAML file, or a prompt file. No mode flags. No `--prompt` vs `--pipeline` distinction.
2. **Everything is a pipeline.** A "loop" is a single-stage pipeline. The engine always builds a `Pipeline` object internally, whether the spec is `ralph` or `refine.yaml`. Same code path for everything.
3. **Progressive complexity.** Level 0: `ap run ralph my-session`. Level 1: add flags (`-n 25 --provider codex`). Level 2: chain stages (`"a:5 -> b:3"`). Level 3: YAML for parallel blocks and custom routing. You never see the next level until you need it.
4. **The stage directory IS the library.** The existing `scripts/stages/` — with `stage.yaml` + `prompt.md` — is the prompt library. No second config format. `ap list` scans it.
5. **One template syntax.** `${VAR}` for everything. No `{{.param}}` Go templates.
6. **Agents and humans speak the same language.** The spawn signal takes a `run` field using the same spec syntax as the CLI. No API vs CLI distinction.
7. **Agents talk back.** The signals protocol lets agents communicate structured requests to the runner — inject context, spawn children, escalate to humans. Not just `continue`/`stop`/`error`.
8. **Ship vertical slices.** Every milestone produces something runnable. No bottom-up package building.

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                       CLI (ap)                            │
│                                                          │
│  ap run ralph my-session                                 │
│  ap run "improve-plan:5 -> refine-tasks:5" my-session   │
│  ap run refine.yaml my-session                           │
│  ap list / ap status / ap resume                         │
│                                                          │
│  Spec Resolution:                                        │
│    stage name → 1-stage pipeline                         │
│    name:N     → 1-stage with count                       │
│    a -> b     → multi-stage chain                        │
│    *.yaml     → full pipeline definition                 │
│    *.md       → prompt file → 1-stage pipeline           │
│                                                          │
└────────────────────────┬─────────────────────────────────┘
                         │
                         ▼
┌──────────────────────────────────────────────────────────┐
│                  Iteration Runner                         │
│                  (internal/runner)                        │
│                                                          │
│  for i := 1; i <= n; i++ {                               │
│    ctx := context.Generate(session, i)                   │
│    prompt := resolve.Template(promptMd, ctx)             │
│    result := provider.Execute(prompt)                    │
│    state.MarkCompleted(i)                                │
│    events.Append("iteration.completed", result)                │
│                                                          │
│    // Process agent signals                              │
│    if result.AgentSignals != nil {                       │
│      signals.Process(result) → inject / spawn /          │
│                                 escalate                 │
│    }                                                     │
│                                                          │
│    if termination.ShouldStop(i, result) { break }       │
│  }                                                       │
│                                                          │
└────────────────────────┬─────────────────────────────────┘
                         │
              ┌──────────┼──────────┐
              ▼          ▼          ▼
         ┌────────┐ ┌────────┐ ┌────────┐
         │ Claude │ │ Codex  │ │ Custom │
         │Provider│ │Provider│ │Provider│
         └────────┘ └────────┘ └────────┘

Signal dispatch (when handlers are configured):
  inject   → prepend to next iteration's ${CONTEXT}
  spawn    → session.Start() (child session via Launcher, separate process)
  escalate → stdout (always) + webhook / exec (optional)
```

**Termination precedence** (runner owns this, enforced centrally):

1. `escalate` signal → session pauses immediately, regardless of other conditions
2. Provider execution failure → `iteration.failed` + retry policy decides retry or stop
3. Agent decision `error` → stop stage (unless stage policy says retry)
4. Agent decision `stop` → stop stage early (even if `-n` not reached)
5. Judgment consensus → stop stage/pipeline
6. Fixed iteration count reached → stop

Higher-numbered rules are only evaluated if no higher-priority rule fires.

**Key change from Rev 1:** The iteration loop lives in `internal/runner`, NOT in `internal/engine`. The engine stays as a provider registry. The runner is the orchestrator that wires everything together. This was flagged by the architecture review — expanding a 62-line registry into the most complex component is bad separation of concerns.

---

## What Already Exists

The `work/gorewrite` branch has **10 production-ready packages**:

| Package | Lines | Status |
|---------|-------|--------|
| `internal/exec` | 443 | Production-ready. Process execution, signal cascade, bounded I/O. |
| `internal/events` | 167 | Production-ready. Append-only events.jsonl with flock. |
| `internal/state` | 356 | Production-ready. Session lifecycle + crash recovery. Has `ResumeFrom()`. |
| `internal/context` | 694 | Production-ready. v3 context.json generation. |
| `internal/resolve` | 186 | Production-ready. `${VAR}` template substitution. |
| `internal/result` | 197 | Production-ready. Agent output normalization. |
| `internal/termination` | 85 | Production-ready. Fixed iteration strategy only. |
| `internal/validate` | 213 | Production-ready. Security checks. |
| `internal/stage` | 191 | Production-ready. Stage definition resolution. |
| `internal/engine` | 62 | Production-ready. Provider registration (thin wrapper). |
| `pkg/provider` | 119 | SDK types. Has Claude CLI provider at `pkg/provider/claude/cli.go`. |

**Empty stubs** (need implementation): `internal/compile`, `internal/judge`, `internal/parallel`, `internal/lock`, `internal/mock`.

**New packages** (not in original codebase): `internal/runner` (iteration loop orchestrator), `internal/signals` (~250 lines, agent signal parsing + dispatch + handler chain), `internal/session` (session launcher — delegates to Launcher for process creation, used by both CLI and spawn), `internal/spec` (unified spec parser → typed AST).

**`cmd/agent-pipelines/main.go`**: Empty. This is where it all starts.

---

## Pre-Build Cleanup (BLOCKING — do before any feature work)

The code reviews found structural defects that will block M0. Fix these first.

### P1. Unify Provider Interface

**Problem:** `internal/provider.Provider` and `pkg/provider.Provider` are different interfaces with incompatible method signatures (`ExecuteRequest` vs `Request`, `*ExecuteResult` vs `Result`). The Claude CLI provider implements `pkg/provider.Provider`, but the engine uses `internal/provider.Provider`. They cannot work together.

**Fix:** Keep `pkg/provider.Provider` (it's richer — has `DefaultModel()`, bitmask capabilities, separate stdout/stderr). Delete `internal/provider.Provider` and `internal/provider.Registry`. Have `internal/engine` import and use `pkg/provider` types. The `internal/provider/` directory becomes either empty or holds internal helpers, not a competing interface.

### P2. Fix Claude Provider Process Management

**Problem:** `pkg/provider/claude/cli.go` uses raw `cmd.Run()` from `os/exec`, bypassing `internal/exec.Run()`. This means no process group management, no SIGTERM→SIGKILL cascade, no bounded I/O, no cleanup on context cancellation. `ap kill` would leave orphaned Claude processes.

**Fix:** Refactor the Claude provider to use `internal/exec.Run()` for process execution.

### P3. Add YAML Dependency

**Problem:** `go.mod` has zero dependencies. The existing code avoids YAML parsing with line-by-line regex hacks. Every milestone that touches config (which is all of them) needs a real YAML parser.

**Fix:** `go get gopkg.in/yaml.v3`

### P4. Rename Entry Point

**Problem:** `cmd/agent-pipelines/main.go` will produce a binary called `agent-pipelines`, not `ap`. Go convention derives binary name from directory name.

**Fix:** Rename `cmd/agent-pipelines/` → `cmd/ap/`.

### P5. Extract Shared Utility

**Problem:** `fileExists()` is duplicated in 3 packages with inconsistent semantics (one counts directories as "existing", two don't).

**Fix:** Extract to `internal/fsutil/fsutil.go`. One function, consistent behavior.

### P6. Deliver Mock Provider

**Problem:** Every milestone needs test doubles. Without `internal/mock`, every agent will independently create ad-hoc mocks.

**Fix:** Implement `internal/mock` with a `MockProvider` that returns configurable canned responses. Do this before parallel work begins.

---

## Contracts

These are the explicit interface contracts that two independent developers must agree on. If these are ambiguous, implementations will diverge.

### Contract 1: Runner ↔ Provider Interface

The `Provider` interface is frozen in Pre-M0 (P1). This is the exact contract.

**Request (what the runner passes to the provider):**

```go
type ExecuteRequest struct {
    // Required
    Prompt     string            // Fully resolved prompt text (all ${VAR} substituted)
    WorkingDir string            // Always the project root (where .ap/ lives), NOT the session dir
    Timeout    time.Duration     // Default 300s for Claude, 300s for Codex. From stage.yaml or --timeout flag.

    // Optional
    Model      string            // Resolved model name (e.g., "opus", "gpt-5.3-codex:high"). Empty = provider default.
    EnvVars    map[string]string // Additional env vars passed to the provider process
    InputFiles []string          // Paths to input files (for providers that support file passing)
}
```

**Environment variables set by the runner for every provider invocation:**

| Variable | Value | Purpose |
|----------|-------|---------|
| `AP_AGENT` | `1` | Always set. Signals the agent is running inside a pipeline. |
| `AP_SESSION` | session name | Current session name |
| `AP_STAGE` | stage name | Current stage type |
| `AP_ITERATION` | iteration number (string) | Current 1-based iteration |

These are set via `ExecuteRequest.EnvVars` and passed through to the provider process. Template variables (`${CTX}`, etc.) are NOT set as env vars — they are substituted into the prompt text before execution.

**Response (what the provider returns to the runner):**

```go
type ExecuteResult struct {
    ExitCode    int       // Process exit code (0 = success)
    Stdout      string    // Full stdout (capped at 1MB by internal/exec)
    Stderr      string    // Full stderr (capped at 1MB by internal/exec)
    Duration    time.Duration // Measured by the runner, not the provider
    StatusJSON  *AgentStatus  // Parsed from status.json written by the agent, or nil if not found
}
```

**The runner does NOT read stdout/stderr for decision-making.** The agent's decision comes exclusively from `status.json`. Stdout/stderr are captured for debugging (`ap logs`) and `iteration.failed` events only.

**Failure mapping:**

| Condition | Runner behavior | Event emitted | Exit code (if surfaced to CLI) |
|-----------|----------------|---------------|-------------------------------|
| Provider process exits 0, `status.json` found | Normal iteration | `iteration.completed` | — |
| Provider process exits 0, no `status.json` | Treat as error (agent didn't write status) | `iteration.failed` | 10 |
| Provider process exits non-zero | Provider failure | `iteration.failed` | 10 |
| Context timeout fires | Timeout | `iteration.failed` | 11 |
| Provider process killed by signal | Process management failure | `iteration.failed` | 10 |

**Working directory:** Always the project root. The agent navigates to session-specific paths via `context.json` (`paths.session_dir`, `paths.stage_dir`, `paths.status`). This matches the Bash engine behavior.

**Provider-specific flags:**

| Provider | CLI command | Required flags | Optional flags |
|----------|-----------|----------------|---------------|
| Claude | `claude` | `--dangerously-skip-permissions`, `-p` (prompt via stdin) | `--model`, `--output-format` |
| Codex | `codex exec` | `--dangerously-bypass-approvals-and-sandbox`, `--ephemeral`, `-` (prompt via stdin) | `--model`, `--json`, `-o` (output file) |

Both providers receive the prompt via stdin (`printf '%s' "$prompt" | provider-cmd ...`). Both are executed via `internal/exec.Run()` for process group management, signal cascade, and bounded I/O.

### Contract 2: Template Variables

**Authoritative list.** These are the ONLY template variables. All use `${VAR}` syntax. Substitution happens in `prompt.md` only (NOT in `stage.yaml`, NOT in handler argv — those use their own variable expansion).

| Variable | Type | Value | Set by |
|----------|------|-------|--------|
| `${CTX}` | file path | Path to `context.json` for this iteration | Runner (always) |
| `${PROGRESS}` | file path | Path to `progress-{session}.md` | Runner (always) |
| `${STATUS}` | file path | Path where agent must write `status.json` | Runner (always) |
| `${ITERATION}` | integer string | 1-based iteration number (e.g., `"3"`) | Runner (always) |
| `${SESSION_NAME}` | string | Session name | Runner (always) |
| `${CONTEXT}` | inline text | Injected context — from CLI `--context`, env `AP_CONTEXT`, or `inject` signal. Empty string if none. | Runner (optional) |
| `${OUTPUT}` | file path | Path to write output file (only set when `stage.yaml` has `output_path`) | Runner (if configured) |

**Substitution rules:**
- `${VAR}` is replaced with the value. If the variable is not defined, the literal `${VAR}` is left in place (no error, no empty string).
- Newlines in values are preserved as-is (no escaping).
- `$$` is NOT an escape for literal `$` — there is no escape syntax. This matches the Bash engine.
- Substitution is a single pass, left to right. No recursive expansion.

### Contract 3: Stage-to-Stage Data Flow

When stages are chained (via `->` syntax or YAML `inputs.from`), "output flows forward" means:

**What is the "output" of a stage?**
The output is the **last iteration's `output.md` file** at `{session_dir}/{stage_dir}/iterations/{last_iteration}/output.md`. This file is written by the agent (the prompt tells it to write to `${OUTPUT}`) or by the runner (copying the agent's final stdout to `output.md`).

**How is it injected into the next stage?**
Via `context.json`. The next stage's `context.json` includes:

```json
{
  "inputs": {
    "from_stage": {
      "plan": [
        ".ap/runs/my-session/stage-00-plan/iterations/005/output.md"
      ]
    }
  }
}
```

The downstream stage's prompt reads these paths from `${CTX}` and `cat`s them.

**`inputs.select` options:**

| Value | Behavior |
|-------|----------|
| `latest` (default) | Only the last completed iteration's `output.md` |
| `all` | All iterations' `output.md` files, in order |

**`inputs.from_parallel` behavior:**
For parallel blocks, each provider's output is keyed by provider name:

```json
{
  "inputs": {
    "from_parallel": {
      "claude": [".ap/runs/.../providers/claude/stage-01-iterate/iterations/003/output.md"],
      "codex": [".ap/runs/.../providers/codex/stage-01-iterate/iterations/002/output.md"]
    }
  }
}
```

Downstream stages iterate over providers: `jq -r '.inputs.from_parallel | to_entries[] | .value[]' ${CTX} | xargs cat`

**Chain default:** When using `->` syntax (ad-hoc chains), `inputs.from: previous` and `inputs.select: latest` are implicit. Each stage gets the previous stage's last output automatically.

### Contract 4: Internal Runner Entrypoint (`ap _run`)

`ap _run` is a **hidden subcommand** — not shown in help, not subject to fuzzy matching. It is the deterministic entrypoint that all Launchers invoke to start the long-running runner process.

**Invocation:**
```bash
ap _run --session <name> --request <path>
```

**`run_request.json` schema:**
```json
{
  "schema_version": 1,
  "spec": {
    "type": "single",
    "stage": "ralph",
    "iterations": 25
  },
  "session": "auth",
  "provider": "claude",
  "model": "opus",
  "input_files": ["docs/plan.md"],
  "context": "Focus on auth module",
  "force": false,
  "parent_session": null
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `schema_version` | yes | Always `1` |
| `spec` | yes | Pre-parsed spec object (from `internal/spec.Parse()`) |
| `spec.type` | yes | `"single"`, `"chain"`, or `"yaml"` |
| `spec.stage` | for single | Stage name |
| `spec.iterations` | no | Override iteration count (from `-n` flag or `stage:N` syntax) |
| `spec.stages` | for chain | Array of `{stage, iterations}` objects |
| `spec.yaml_path` | for yaml | Absolute path to pipeline YAML file |
| `session` | yes | Session name |
| `provider` | no | Provider override (default: from stage.yaml) |
| `model` | no | Model override |
| `input_files` | no | Absolute paths to input files |
| `context` | no | Injected context text |
| `force` | no | Whether `--force` was specified |
| `parent_session` | no | Set when spawned by another session |

**Lifecycle:**
1. `ap run ralph auth` → parses spec, resolves stage, writes `run_request.json` atomically to `.ap/runs/auth/run_request.json`
2. Launcher invokes: `ap _run --session auth --request .ap/runs/auth/run_request.json`
3. `ap _run` reads `run_request.json`, acquires lock, creates state, opens event writer, signals readiness, runs the iteration loop
4. `ap resume auth` → reads existing `run_request.json` + `state.json`, writes updated request if `--context` provided, Launcher invokes `ap _run --session auth --request ... --resume`

**Why this exists:**
- Launchers need a deterministic command to invoke (no fuzzy parsing, no spec resolution, no human UX)
- `ap resume` reuses the same entrypoint with `--resume` flag
- Spawn signal uses the same entrypoint (child session gets its own `run_request.json`)
- The request file is a durable record of what was asked — useful for debugging and crash recovery

### Contract 5: Event Schema (Required Fields)

**Common fields (required on ALL events):**

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Event type (e.g., `session.started`, `iteration.completed`) |
| `timestamp` | ISO 8601 | When the event was emitted |
| `session` | string | Session name |

**Per-event required fields:**

| Event Type | Additional Required Fields |
|------------|--------------------------|
| `session.started` | `stage`, `provider`, `model`, `max_iterations` |
| `session.completed` | `total_iterations`, `duration_ms`, `final_status` (completed/failed) |
| `iteration.started` | `stage`, `node_id`, `iteration`, `provider`, `model` |
| `iteration.completed` | `stage`, `node_id`, `iteration`, `duration_ms`, `decision`, `files_touched` (array), `summary` |
| `iteration.failed` | `stage`, `node_id`, `iteration`, `duration_ms`, `exit_code`, `error` (string), `stderr_tail` (last 500 chars) |
| `signal.inject` | `iteration`, `text` (the injected context) |
| `signal.dispatching` | `iteration`, `signal_id` (deterministic: `sig-{iter}-{type}-{idx}`), `signal_type` (`spawn`/`escalate`) |
| `signal.spawn` | `iteration`, `signal_id`, `child_session`, `child_stage`, `child_pid` |
| `signal.spawn.failed` | `iteration`, `signal_id`, `error` |
| `signal.escalate` | `iteration`, `signal_id`, `reason`, `options` (array, may be empty) |
| `signal.escalate.failed` | `iteration`, `signal_id`, `error` |
| `signal.handler.error` | `iteration`, `signal_id`, `handler_type` (`webhook`/`exec`), `error` |

**Ordering guarantees:**
- `session.started` is always the first event
- `iteration.started` always precedes `iteration.completed` or `iteration.failed` for the same iteration
- `signal.dispatching` always precedes its result event (`signal.spawn`, `signal.spawn.failed`, etc.)
- `session.completed` is always the last event (when the session ends normally)
- Events are strictly ordered by `timestamp` within a session's `events.jsonl`

### Contract 6: Signal Handler Payloads

**Webhook payload schema (HTTP POST, `Content-Type: application/json`):**

```json
{
  "type": "escalate",
  "session": "auth",
  "iteration": 5,
  "stage": "ralph",
  "reason": "Two valid auth architectures. Need human decision.",
  "options": ["JWT stateless", "Session-based with Redis"],
  "callback_url": "http://127.0.0.1:41823/resume",
  "callback_token": "a1b2c3d4e5f6...",
  "timestamp": "2026-02-27T10:05:45Z"
}
```

| Field | Always present | Description |
|-------|---------------|-------------|
| `type` | yes | Signal type (`escalate`, `spawn`) |
| `session` | yes | Session name |
| `iteration` | yes | Iteration number |
| `stage` | yes | Current stage name |
| `reason` | for escalate | Escalation reason text |
| `options` | for escalate | Array of options (may be empty) |
| `child_session` | for spawn | Child session name |
| `child_stage` | for spawn | Child stage name |
| `callback_url` | if listener is active | URL to POST resume response |
| `callback_token` | if callback_url is non-localhost | One-time auth token |
| `timestamp` | yes | ISO 8601 timestamp |

**Exec handler variable expansion:**

Available variables in `argv` arrays:

| Variable | Value |
|----------|-------|
| `${SESSION}` | Session name |
| `${STAGE}` | Current stage name |
| `${ITERATION}` | Iteration number (string) |
| `${REASON}` | Escalation reason (for escalate signals) |
| `${CHILD_SESSION}` | Child session name (for spawn signals) |
| `${TYPE}` | Signal type |

**Rules:**
- Missing variables expand to empty string (no error)
- No shell expansion — `argv` is passed directly to `exec`, NOT through a shell
- Variables are only expanded in `argv` values, not in the command name (first element)
- Handler timeout: 30s default, configurable per-handler in config. On timeout: `signal.handler.error` event, fall through to next handler.
- Retry: no automatic retry. If a handler fails, it's logged and skipped. The `stdout` handler is always the final backstop and cannot fail.

---

## Prompt Discovery (No Library.yaml)

**Rev 1 proposed** a `library.yaml` with categories, params, enums, and a separate `prompts/` directory. All three reviewers flagged this as over-engineered and a duplication risk.

**Rev 2 approach:** The existing `scripts/stages/` directory IS the prompt library.

```
scripts/stages/
├── elegance/
│   ├── stage.yaml    # name, description, termination config, provider defaults
│   └── prompt.md     # the prompt template (uses ${VAR} syntax)
├── ralph/
│   ├── stage.yaml
│   └── prompt.md
├── improve-plan/
│   ├── stage.yaml
│   └── prompt.md
└── ... (26 stages total)
```

`ap list` scans this directory and prints name + description from each `stage.yaml`.
`ap run elegance my-session` resolves to `scripts/stages/elegance/prompt.md`.

The existing `internal/stage` package already does this resolution with multi-precedence search (project root → plugin dir → builtins). We just wire it to the CLI.

**No new config format.** No `library.yaml`. No prompt copies. No dual-template syntax. If we want parameterization later, we add `params:` to `stage.yaml` and resolve them as additional `${VAR}` entries — one syntax, one resolution pass.

**Prompt files are embedded** in the binary via `go:embed` for the built-in stages. User stages in the local project's `.ap/stages/` or `scripts/stages/` override built-ins. Same precedence the Bash engine uses today.

---

## CLI Design

Seven commands. One positional spec. Eight modifier flags. That's the v1 surface.

### Commands

```bash
# === Run (the main command) ===
ap run ralph my-session                                # stage name
ap run ralph:25 my-session                             # stage + iteration count
ap run ralph my-session --provider codex -m gpt-5.3-codex:high   # override provider/model
ap run "improve-plan:5 -> refine-tasks:5" my-session   # ad-hoc chain
ap run refine.yaml my-session                          # YAML pipeline
ap run ./custom.md -n 5 my-session                     # custom prompt file
ap run ralph my-session --fg                          # foreground (rare: CI/testing)
ap run ralph my-session --context "Focus on auth"      # inject context
ap run ralph my-session --input docs/plan.md           # pass input files

# === List (discovery) ===
ap list                   # show all available stages
ap list --json            # machine-readable output
ap list --verbose         # show stages with full descriptions

# === Status (session management) ===
ap status                 # all sessions
ap status my-session      # specific session details
ap status --json          # machine-readable output

# === Resume (separate from run — never prompts) ===
ap resume my-session                          # resume crashed/paused session
ap resume my-session --context "Go with JWT"  # resume with injected context

# === Session lifecycle ===
ap kill my-session        # kill session + release lock + mark state failed
ap logs my-session        # tail events.jsonl
ap logs my-session -f     # follow (live tail)
ap logs my-session --json # structured event output
ap clean my-session       # remove session dir + lock + tmux session
ap clean --all            # clean all completed/failed sessions
```

### `ap run` Spec Resolution

The first positional argument is **what to run**. `ap` resolves it automatically:

| Input | Resolution |
|-------|-----------|
| `ralph` | Stage name → single-stage pipeline, default iterations from `stage.yaml` |
| `ralph:25` | Stage name + explicit iteration count |
| `"improve-plan:5 -> refine-tasks:5"` | Chain expression → multi-stage pipeline, outputs flow forward |
| `refine.yaml` | Ends in `.yaml` → parse as pipeline definition (supports parallel blocks, custom routing) |
| `./custom.md` | File path → prompt file → single-stage pipeline |

All five produce the same internal `Pipeline` object. The engine doesn't know or care how it was specified.

**Unified spec parser** (`internal/spec`): The spec string is parsed into a typed AST, not matched by heuristic string rules. `spec.Parse(input)` returns one of:

- `ChainSpec{Stages: []StageRef}` — contains `->` operator
- `FileSpec{Path, Kind: yaml|prompt}` — ends with `.yaml`/`.yml`/`.md`, or starts with `./`/`/`
- `StageSpec{Name, Count}` — stage name with optional `:N` count
- Each type has a `.ToPipeline()` method that produces the same `Pipeline` object

**Resolution precedence** (parser rules, first match wins):
1. Contains `->` → parse as chain expression → `ChainSpec`
2. Ends with `.yaml` or `.yml` → `FileSpec{Kind: yaml}`
3. Ends with `.md` or starts with `./` or `/` → `FileSpec{Kind: prompt}`
4. Contains `:` → parse as `name:N` → `StageSpec{Name, Count}`
5. Otherwise → `StageSpec{Name}` → stage name lookup (project `scripts/stages/` → built-in stages)

**Error rules:**
- If a file path doesn't exist, exit with error (never fall through to stage lookup)
- If a stage name matches both a project stage and a built-in stage, project wins (explicit override)
- If spec matches no rule, exit with error + suggestion
- Parse errors are specific: `"invalid chain: expected stage name after '->'"` not `"invalid spec"`

### `ap run` Flags

| Flag | Short | Description |
|------|-------|-------------|
| `-n COUNT` | | Max iterations (override for spec or stage.yaml default) |
| `--provider NAME` | | Provider: `claude`, `codex` (default: from stage.yaml or `claude`) |
| `--model NAME` | `-m` | Model override (e.g., `sonnet`, `gpt-5.3-codex:high`) |
| `--input PATH` | `-i` | Input file(s), repeatable |
| `--context TEXT` | `-c` | Injected context text (appears as `${CONTEXT}` in prompt) |
| `--force` | `-f` | Override existing session (kills it first) or bypass safety checks on `clean` |
| `--foreground` | `--fg` | Run in current terminal instead of background (for CI/testing) |
| `--explain-spec` | | Dry-run: show how the spec was parsed (type, resolution path, pipeline shape) and exit |

**`--force` semantics per command:**

| Command | Without `--force` | With `--force` |
|---------|-------------------|----------------|
| `ap run` on existing session | Exit 4 (`SESSION_EXISTS`) with guidance: "use `--force` to replace or `ap resume` to continue" | Kill existing session, start fresh |
| `ap clean` on running/paused session | Refuse with error: "session is still running/paused" | Kill + clean |
| `ap clean --all` | Skip running/paused sessions | Clean everything including running/paused |

### Robot Mode (Agent-Friendly CLI)

`ap` is used BY agents, not just FOR agents. Every command is designed for machine consumption first, human pretty-printing second. This section defines the full agent-friendly CLI contract.

#### Output Mode Detection

```
Decision tree (checked in order):
  --json flag present?          → JSON mode
  stdout is not a TTY (piped)?  → JSON mode
  AP_OUTPUT=json env var?       → JSON mode
  otherwise                     → human mode
```

One `term.IsTerminal()` check at startup. Stored as `OutputMode` enum (`Human | JSON`) threaded through all formatters. In JSON mode: all output to stdout as valid JSON, no ANSI codes, no spinners.

#### Global `--json` Flag

Every command supports `--json` for machine-parseable output:

| Command | JSON output |
|---------|------------|
| `ap` (no args) | `{"version":"0.1.0","commands":[...],"command_aliases":{...}}` |
| `ap run ... --json` | `{"session_id":"name","status":"started","spec":{...},"paths":{...}}` |
| `ap status auth --json` | Full session state as structured JSON |
| `ap list --json` | `{"stages":[...],"pipelines":[...]}` |
| `ap logs auth --json` | JSONL event stream |
| `ap resume auth --json` | `{"session_id":"name","status":"resumed","from_iteration":6}` |
| `ap kill auth --json` | `{"session_id":"name","status":"killed","was_running":true}` |
| `ap clean auth --json` | `{"cleaned":["name"],"skipped":[],"freed_bytes":1048576}` |

`ap kill` and `ap clean` are **idempotent** — killing a dead session or cleaning an already-clean session returns exit 0. Don't punish agents for retrying.

#### Exit Codes

Agents read exit codes BEFORE parsing output. Exit 0 = parse success payload. Non-zero = parse error payload.

**Global exit code set:**

| Code | Meaning | When |
|------|---------|------|
| 0 | Success | Operation completed (including fuzzy-matched commands) |
| 1 | General error | Unclassified runtime failure |
| 2 | Invalid arguments | Bad flags, missing required args, invalid spec, ambiguous command |
| 3 | Not found | Session, stage, or file doesn't exist |
| 4 | Already exists | Session already running (use `--force` or `ap resume`) |
| 5 | Locked | Session locked by another process |
| 10 | Provider error | Claude/Codex process failed |
| 11 | Timeout | Operation exceeded time limit |
| 20 | Paused | Session paused (needs human input via `ap resume`) |

**Per-command exit code overrides (idempotency rules):**

| Command | Target doesn't exist | Target already in desired state |
|---------|---------------------|-------------------------------|
| `ap run` | Exit 3 (`STAGE_NOT_FOUND` or `FILE_NOT_FOUND`) | Exit 4 (`SESSION_EXISTS`) |
| `ap status` | Exit 3 (`SESSION_NOT_FOUND`) | — |
| `ap resume` | Exit 3 (`SESSION_NOT_FOUND`) | Exit 0 if already running (no-op) |
| `ap kill` | **Exit 0** (`"was_running": false`) | Exit 0 (already dead) |
| `ap clean` | **Exit 0** (`"cleaned": []`) | Exit 0 (already clean) |
| `ap logs` | Exit 3 (`SESSION_NOT_FOUND`) | — |

**Principle:** Destructive/cleanup commands (`kill`, `clean`) are idempotent — agents can safely retry without error handling. Query/continuation commands (`status`, `resume`, `logs`) report not-found because the agent needs to know the session doesn't exist.

#### Structured Error Format

Every non-zero exit produces a structured error. In JSON mode, it's the JSON object below. In human mode, it's formatted text containing the same semantic information (error code, message, suggestions). The goal: **the error message alone is sufficient for the agent to construct the correct command on its next try.**

```json
{
  "error": {
    "code": "STAGE_NOT_FOUND",
    "message": "Stage 'ralf' not found",
    "detail": "No stage named 'ralf'. Closest match: 'ralph' (distance 1)",
    "syntax": "ap run <spec> <session> [flags]",
    "suggestions": [
      "ap run ralph auth          # the Ralph implementation loop",
      "ap run ralph:25 auth       # with explicit iteration count",
      "ap list --json             # see all available stages"
    ],
    "available_stages": ["ralph", "elegance", "improve-plan", "refine-tasks", "..."]
  }
}
```

Error codes are SCREAMING_SNAKE constants: `INVALID_SPEC`, `MISSING_ARGUMENT`, `SESSION_NOT_FOUND`, `STAGE_NOT_FOUND`, `FILE_NOT_FOUND`, `SESSION_EXISTS`, `SESSION_LOCKED`, `PROVIDER_FAILED`, `PROVIDER_TIMEOUT`, `SESSION_PAUSED`, `UNKNOWN_COMMAND`, `AMBIGUOUS_COMMAND`, `INVALID_FLAG`.

Every error includes: (1) what went wrong, (2) why, (3) canonical syntax, (4) 2-3 concrete example commands the agent likely meant, (5) available options when the error is about an invalid choice.

#### Fuzzy Command Matching

Agents hallucinate syntax. They confuse flag formats, swap argument order, use aliases that don't exist, and mix conventions from other CLIs. When the intent is legible, **execute the command** — but include a correction hint so the agent learns the canonical form. When it's ambiguous, fail with a detailed tutorial.

**The guiding rule:** if there's high confidence we know what the agent meant, execute with correction. If not, fail with maximum helpfulness.

**Layer 1: Command synonyms** — exact alias map:

| Alias | Maps to |
|-------|---------|
| `start`, `launch`, `exec`, `execute` | `run` |
| `ls`, `show`, `stages`, `pipelines` | `list` |
| `info`, `state`, `check` | `status` |
| `stop`, `terminate`, `cancel`, `abort` | `kill` |
| `rm`, `remove`, `delete`, `prune` | `clean` |
| `continue`, `restart` | `resume` |
| `tail`, `watch`, `follow` | `logs` |

**Layer 2: Levenshtein fuzzy match** — for typos (threshold: distance 2 for commands/stages 5+ chars, distance 1 for 4 chars or fewer):

| Agent types | Resolves to | Hint |
|-------------|-------------|------|
| `ap staus auth` | `ap status auth` | `hint: did you mean 'ap status'?` |
| `ap lis` | `ap list` | `hint: expanded 'lis' to 'ap list'` |
| `ap run ralf auth` | `ap run ralph auth` | `hint: did you mean stage 'ralph'?` |

**Layer 3: Flag normalization** — agents get flag syntax wrong constantly:

| Agent writes | Normalized to | Hint |
|-------------|--------------|------|
| `--iterations 5` | `-n 5` | `hint: use '-n 5' for iteration count` |
| `--max-iterations=5`, `--count 5`, `--runs 5` | `-n 5` | same |
| `--foreground` | `--fg` | `hint: shorthand is '--fg'` |
| `--output json`, `--format=json` | `--json` | `hint: use '--json'` |
| `--provider anthropic` | `--provider claude` | `hint: provider name is 'claude'` |
| `--provider openai` | `--provider codex` | `hint: provider name is 'codex'` |
| `-n=5` | `-n 5` | (silently accept) |

**Layer 4: Argument order recovery** — if the first positional arg doesn't resolve as a spec but the second one does, swap them:

```
ap run auth ralph  →  ap run ralph auth
  hint: spec comes before session name. use 'ap run ralph auth'
```

**Layer 5: Spec syntax recovery** — common misformats:

| Agent writes | Resolved to | Hint |
|-------------|-------------|------|
| `ap run ralph 25 auth` | `ap run ralph:25 auth` | `hint: use 'ralph:25' (colon) for iteration count` |
| `ap run ralph > elegance auth` | `ap run "ralph -> elegance" auth` | `hint: chain stages with '->' not '>'` |
| `ap run ralph, elegance auth` | `ap run "ralph -> elegance" auth` | `hint: use '->' to chain, not commas` |
| `ap run ./scripts/stages/ralph/ auth` | `ap run ralph auth` | `hint: use stage name 'ralph', not directory path` |

**Layer 6: Stage name recovery** — when a stage name doesn't match, try:
1. Strip directory prefixes (`scripts/stages/`, `stages/`, `./`)
2. Strip file extensions (`.yaml`, `.yml`, `.md`)
3. Levenshtein match against known stage names
4. Keyword-to-category match for descriptive words (`"implement"` → ralph, codex-work; `"review"` → elegance, fresh-eyes; `"test"` → test-scanner, test-review; etc.)

#### Corrections in Success Responses

When fuzzy matching resolves a command, the response includes a `corrections[]` array. Exit code is still 0 (success). This teaches agents the canonical form without breaking their workflow.

```json
{
  "session_id": "auth",
  "status": "started",
  "spec": {"type": "single", "stage": "ralph", "iterations": 25},
  "corrections": [
    {"from": "start", "to": "run", "hint": "'start' is a synonym for 'ap run'"},
    {"from": "--iterations 25", "to": "-n 25", "hint": "use '-n 25' for iteration count"},
    {"from": "--provider anthropic", "to": "--provider claude", "hint": "provider name is 'claude'"}
  ]
}
```

Agents that don't care about learning can ignore `corrections[]` entirely — it's always an array (empty if no corrections).

#### No-Args Help (~100 tokens)

`ap` with no arguments prints compact help designed for LLM context windows:

**M0a version** (before fuzzy matching ships in M0b):

```
ap - agent pipeline runner

Commands:
  run <spec> <session>   Run a pipeline
  list                   Show available stages
  status [session]       Check session state
  resume <session>       Resume paused/crashed session
  kill <session>         Terminate a session
  logs <session>         Tail session events
  clean [session]        Remove completed sessions

Spec: ralph | ralph:25 | "a:5 -> b:5" | file.yaml | ./prompt.md
Flags: -n COUNT  --provider NAME  -m MODEL  -i INPUT  -c CONTEXT  -f  --fg  --json

Run 'ap <command> --help' for details.
```

**M0b version** (after fuzzy matching ships — adds the "Forgiving" line):

```
ap - agent pipeline runner

Commands:
  run <spec> <session>   Run a pipeline
  list                   Show available stages
  status [session]       Check session state
  resume <session>       Resume paused/crashed session
  kill <session>         Terminate a session
  logs <session>         Tail session events
  clean [session]        Remove completed sessions

Spec: ralph | ralph:25 | "a:5 -> b:5" | file.yaml | ./prompt.md
Flags: -n COUNT  --provider NAME  -m MODEL  -i INPUT  -c CONTEXT  -f  --fg  --json

Forgiving: typos, synonyms (start=run, ls=list, stop=kill), flag aliases auto-corrected.
Run 'ap <command> --help' for details.
```

JSON mode includes the synonym map so agents can discover valid aliases without trial-and-error:
```json
{
  "version": "0.1.0",
  "commands": ["run", "list", "status", "resume", "kill", "logs", "clean"],
  "command_aliases": {"start": "run", "ls": "list", "stop": "kill", "...": "..."}
}
```

#### AGENTS.md

Ship an `AGENTS.md` in the project root explaining the CLI contract for AI agents:

- Quick reference table (what you want → command)
- Output contract (exit 0 = parse payload + optional corrections, non-zero = parse error.suggestions)
- Forgiving syntax summary (synonyms, typos, flag aliases, argument order)
- Common agent patterns (start → poll status → read result)
- Spec types with examples

This is the first file an agent reads when interacting with `ap`. Keep it under 100 lines.

**What's NOT here** (and why):
- `--prompt`, `--prompt-file`, `--pipeline`: Replaced by positional spec resolution. No mode flags needed.
- `--set`, `--termination`, `--consensus`, `--min-iterations`, `--command`: Read from `stage.yaml`. Override via env vars for the rare case.
- `ap resume`: Separate command for resuming paused/crashed sessions. `ap run` on an existing session exits with error + guidance (not a prompt — safe for non-interactive use).
- `ap library render`: YAGNI. Read the markdown file if you want to see it.

### Signal Handling

When the `ap` binary receives SIGINT (Ctrl+C) or SIGTERM:
1. Mark session state as `failed` with resume guidance
2. Send SIGTERM to the active provider process (via `internal/exec` cascade)
3. Wait for graceful shutdown (up to 5s)
4. Release session lock
5. Exit

This is a correctness requirement, not a feature. Build it into M0.

### Execution Model

**Background by default.** `ap run` launches the session in the background and returns immediately. This matches how the Bash engine works today. You never need to attach to anything to know what's happening — `ap status` is the monitoring interface.

`--fg` runs in the current terminal (foreground mode) for CI pipelines, testing, and debugging. This is the exception, not the default.

**Pluggable Launcher interface:** Background execution is abstracted behind a `Launcher` interface:

```go
type Launcher interface {
    Start(session string, runnerCmd []string, opts LaunchOpts) (SessionHandle, error)
    Kill(session string) error
}
```

Two implementations:
- **`TmuxLauncher`** (default when tmux is available): Spawns `ap-{session}` tmux session. Users can `tmux attach -t ap-{session}` directly for live view (not part of the `ap` CLI surface).
- **`ProcessLauncher`** (fallback when tmux is unavailable): Detached child process with PID file. `ap status`/`ap logs` work identically. Used automatically in CI/Docker/headless environments.

tmux is preferred but not required. If tmux is missing, `ap run` logs a one-time notice (`"tmux not found — using process launcher"`) and continues. No hard error.

**Startup confirmation:** Every `ap run` is a two-phase operation — each Launcher uses its own readiness mechanism (no file polling):

- **`TmuxLauncher`**: Uses `tmux wait-for` channels. Parent blocks on `tmux wait-for ap-ready-{session}`. Child signals `tmux wait-for -S ap-ready-{session}` after init. tmux-native, no files, no FD inheritance needed.
- **`ProcessLauncher`**: Uses `os.Pipe()` with `cmd.ExtraFiles`. Parent creates pipe, child inherits FD via fork/exec, writes `{"status":"ready"}` after init. Works because `ProcessLauncher` uses real fork/exec, not tmux.

Both: child initializes (acquire lock → create state → open event writer → write `session.started` event) then signals readiness. Parent blocks with timeout (default 5s, configurable). Return exit 0 + session info if ready; exit 1 + error if not.

> **Why not `os.Pipe()` for tmux?** tmux creates a new process tree via its server — pipe FDs from the parent process are NOT inherited across `tmux new-session`. This was caught in premortem review. Each Launcher uses the mechanism that works for its execution model.

### Monitoring (`ap status`)

`ap status` is the primary way to observe running sessions. It reads the pre-computed `state.json` snapshot — no tmux attachment needed, no event log scanning.

**O(1) status via incremental snapshot:** The runner maintains `state.json` as a single pre-computed snapshot, updated atomically (write-to-temp + rename in same directory) after each event append. This file contains everything `ap status` needs: current stage, iteration, recent activity (last 5 iterations), aggregate file counts, signal history, and child session status. `ap status` reads this one file regardless of session length — a 500-iteration session is just as fast as a 5-iteration session. On crash, `state.json` is regenerated from `events.jsonl`.

```
$ ap status auth
Session: auth (running)
Stage:   ralph (iteration 4/25)
Provider: claude (opus)
Started: 2m 13s ago
Current iteration: running for 38s

Recent activity:
  [iter 1] 45s → continue | 3 files modified
  [iter 2] 38s → continue | 2 files modified, spawned auth-tests
  [iter 3] 41s → continue | 1 file modified

Children:
  auth-tests (test-scanner, iter 2/5, running)

Files touched (this session):
  src/auth/jwt.ts (modified 3x)
  src/auth/middleware.ts (created)
  tests/auth.test.ts (modified 2x)

Signals:
  spawn: auth-tests (handled, iter 2)
```

`ap status --json` returns the same data as structured JSON for agent consumption.

### Iteration Telemetry

Every completed iteration emits an `iteration.completed` event to `events.jsonl`:

```json
{
  "type": "iteration.completed",
  "timestamp": "2026-02-27T10:05:45Z",
  "stage": "ralph",
  "node_id": "plan",
  "iteration": 3,
  "provider": "claude",
  "model": "opus",
  "duration_ms": 41200,
  "decision": "continue",
  "reason": "JWT validation middleware implemented, tests passing",
  "files_touched": ["src/auth/jwt.ts", "src/auth/middleware.ts"],
  "signals_emitted": [],
  "summary": "Implemented JWT validation middleware"
}
```

`stage` and `node_id` are included in all iteration events so `ap status` and `ap logs` can disambiguate multi-stage and parallel runs. For single-stage loops, `node_id` equals `stage`.

The runner gets `files_touched` and `summary` directly from the agent's `status.json` (`work.files_touched` and `summary` fields). No created/modified split — just `files_touched`. Duration is tracked by the runner itself. This is what powers `ap status` and `ap logs`.

---

## Multi-Stage Pipelines

Everything is a pipeline. A "loop" is a single-stage pipeline. There are two ways to define multi-stage pipelines:

### Ad-Hoc Chains (CLI)

For simple sequential chains, use `->` syntax directly on the command line:

```bash
ap run "improve-plan:5 -> refine-tasks:5" my-session
```

`->` means "output flows forward." Each stage automatically receives the previous stage's output as input. This covers the 80% case — no YAML file needed.

Under the hood, the chain builds a `Pipeline` object with sequential nodes and default input passing (`inputs.from: previous`). It's a convenience layer over the same pipeline engine.

### YAML Pipelines (for complex cases)

For parallel blocks, custom input routing, per-stage overrides, or reusable definitions:

```yaml
# refine.yaml — plan refinement then task refinement
name: refine
nodes:
  - id: plan
    stage: improve-plan
    runs: 5
  - id: tasks
    stage: refine-tasks
    runs: 5
    inputs:
      from: plan
```

```yaml
# compare.yaml — parallel providers then synthesis
name: compare
nodes:
  - id: dual-refine
    parallel:
      providers: [claude, codex]
      stages:
        - id: iterate
          stage: improve-plan
          termination: { type: judgment, consensus: 2, max: 5 }
  - id: synthesize
    stage: elegance
    inputs:
      from_parallel: iterate
```

### When to use which

| Scenario | Spec |
|----------|------|
| Single loop | `ap run ralph my-session` |
| Quick 2-3 stage chain | `ap run "a:5 -> b:5" my-session` |
| Reusable pipeline | `ap run refine.yaml my-session` |
| Parallel providers | `ap run compare.yaml my-session` (YAML required) |
| Custom input routing | YAML required |

**Build order:** Single-stage loops (M0a-M0c) are built first because pipelines depend on them. Chain parsing and multi-stage orchestration (M3) and parallel blocks (M4) follow immediately — they are core features, not stretch goals. The Bash engine handles pipelines during development, but the goal is full replacement.

---

## Agent Signals Protocol

Today, agents communicate with the runner via 3 decisions in `status.json`: `continue`, `stop`, `error`. That's a walkie-talkie with 3 buttons. The signals protocol extends this with 3 structured signal types that let agents participate in orchestration decisions.

### The 3 Signals

#### 1. `inject` — Pass critical context to the next iteration

Ephemeral inter-iteration communication. Unlike progress.md (which accumulates forever), injected context is consumed by the next iteration and cleared.

```json
{
  "decision": "continue",
  "agent_signals": {
    "inject": "CRITICAL: The JWT library defaults to RS256 but config expects HS256. Fix config BEFORE touching auth code."
  }
}
```

**Runner behavior:** Prepend the inject text to the next iteration's `${CONTEXT}` variable. Clear after consumption.

#### 2. `spawn` — Launch a parallel child session or pipeline

The agent discovers work that should happen independently without blocking the main loop. The `run` field uses the same spec syntax as the CLI — stage name, chain, or YAML file.

```json
{
  "decision": "continue",
  "agent_signals": {
    "spawn": [{
      "run": "test-scanner",
      "session": "auth-tests",
      "context": "Zero test coverage on src/auth/jwt.ts",
      "n": 5
    }]
  }
}
```

Agents can also spawn multi-stage pipelines (requires M3 — chain parsing):

```json
{
  "decision": "continue",
  "agent_signals": {
    "spawn": [{
      "run": "test-scanner:5 -> elegance:3",
      "session": "validate-refactor"
    }]
  }
}
```

**Note:** Until M3 ships, `spawn.run` only accepts stage names and `stage:N` syntax. Chain and YAML specs in spawn are validated and rejected with a clear error until the chain parser is available.

**Runner behavior:** Parse the `run` field using the same spec parser as the CLI (`internal/spec.Parse()`). Execute spawn via the session API: `session.Start(spec, sessionName, opts)` — the same function used by `cmd/ap/run.go`. Track parent→child relationship in events. Parent keeps running.

**Session API** (`internal/session`): Both the CLI and spawn share one entry point:

```go
func Start(spec spec.Spec, session string, opts StartOpts) (*Session, error)
```

`session.Start()` delegates to the Launcher (TmuxLauncher or ProcessLauncher) to create the child as a **separate process** — the same mechanism used by `ap run`. The child is fully independent: it has its own PID, its own session directory, its own event log. The parent tracks children via `state.json` and event log entries, not Go channels or shared memory. This means children **survive parent crashes** and can be adopted on resume.

**Non-interactive safety:** `session.Start()` never prompts. If the session already exists in any state, it returns an error. Spawn always creates a fresh session with a unique name.

**Spawn confirmation:** The parent uses the same per-Launcher readiness mechanism as `ap run` (tmux `wait-for` or `os.Pipe()`). On success, emit `signal.spawn` event. On failure, emit `signal.spawn.failed` event with the error message. Parent continues running either way.

**Spawn re-dispatch idempotency:** If the runner crashes after launching a child but before writing `signal.spawn`, resume will re-dispatch the spawn. If the child session already exists (from the pre-crash launch), adopt it instead of failing. Check for existing session → if running, adopt; if completed, mark as handled; if failed, mark as failed. Children survive parent crashes because they are independent processes.

**Spawn limits** (configurable, not hardcoded):

| Limit | Default | Description |
|-------|---------|-------------|
| `max_child_sessions` | 10 | Maximum total children a session can spawn |
| `max_spawn_depth` | 3 | Maximum parent→child→grandchild nesting depth |
| `max_concurrent_providers` | 0 (unlimited) | Maximum provider processes running simultaneously. Set in `~/.config/ap/config.yaml` for resource-constrained machines. |

By default, the engine spawns whatever agents request — no artificial caps. On resource-constrained machines (e.g., 8GB VPS), operators can set `max_concurrent_providers` in config to queue excess spawns. The engine is not opinionated about infrastructure — that's the orchestrator's job.

**Child isolation:** Each child runs as a separate process (via the Launcher), fully isolated from the parent. A child crash does not affect the parent — the parent detects it via session state on the next status check. On SIGTERM to the parent, the parent sends SIGTERM to all known child PIDs (tracked in state), waits up to 5s for children to exit, then force-kills any remaining child processes. If the parent crashes, children continue running independently and can be adopted on resume.

#### 3. `escalate` — Request human (or external system) review

The agent hits a decision it shouldn't make alone.

```json
{
  "decision": "stop",
  "agent_signals": {
    "escalate": {
      "type": "human",
      "reason": "Two valid auth architectures. Option A: JWT (simpler). Option B: Sessions with Redis (more secure).",
      "options": ["JWT stateless", "Session-based with Redis"]
    }
  }
}
```

**Runner behavior:** Escalate **always pauses** the session — regardless of `decision`. The agent's `decision` field is overridden to `stop`, and the session transitions to `paused` state. Dispatch to configured signal handlers (see Signal Handlers below). Session resumes when someone runs `ap resume auth --context "Go with JWT for v1"`.

> **Design note:** Non-blocking notifications (e.g., "FYI: found a potential issue") are better handled by `inject` or webhook-based signal handlers on other signal types. Keeping escalate = always-pause is simpler and avoids ambiguity about what "escalate but keep going" means.

### Deferred Signals (v1.1+)

Two additional signal types are reserved in the schema but NOT implemented in v1:

- **`checkpoint`** — Named rollback points. Deferred because git already provides rollback. Agents that emit `checkpoint` in v1 get a logged warning, not an error.

- **`budget`** — Work estimates. Deferred because it's display-only without enforcement. Agents that emit `budget` in v1 get a logged warning, not an error.

### Signal Combination Matrix

Agents can emit multiple signals in a single `status.json`. The runner processes them in order: inject → spawn → escalate.

| Combination | Behavior |
|------------|----------|
| inject only | Prepend to next `${CONTEXT}`, clear after. Normal. |
| spawn only | Start child session(s). Parent continues. |
| escalate only | Pause session. Dispatch to handlers. |
| inject + spawn | Inject is for the PARENT's next iteration, not the child. Child gets its own `context` from the spawn signal's `context` field. |
| inject + escalate | Inject is stored but not consumed until resume (session pauses immediately). On resume, the inject context is prepended to the resumed iteration's `${CONTEXT}`. |
| spawn + escalate | Spawn fires FIRST (children start), then escalate pauses the parent. Children continue running while parent is paused. On resume, parent checks children's status and continues. |
| inject + spawn + escalate | All three: inject stored, spawn fires, then pause. On resume, inject is consumed, children are checked. |

> **Key rule:** Escalate always fires LAST because it pauses the session. Any other signals in the same iteration are processed before the pause takes effect.

### Complete status.json Schema (with signals)

```json
{
  "decision": "continue",
  "reason": "Completed task, more work remaining",
  "summary": "Implemented JWT validation middleware",
  "work": {
    "items_completed": ["bead-042: JWT middleware"],
    "files_touched": ["src/auth/jwt.ts", "src/auth/middleware.ts"]
  },
  "errors": [],
  "agent_signals": {
    "inject": "JWT library uses RS256 by default, update config first",
    "spawn": [{"run": "test-scanner", "session": "auth-tests", "context": "...", "n": 5}],
    "escalate": null
  }
}
```

All fields optional. Agents that don't know about signals work exactly as before. Backward compatible.

### Signal Handlers (Pluggable Notification Chain)

When the runner processes a signal, it dispatches through a chain of handlers. Handlers are pluggable and transport-agnostic.

**Built-in handlers:**

| Handler | What it does | When to use |
|---------|-------------|-------------|
| `stdout` | Print to terminal | Always (default, cannot be disabled) |
| `webhook` | HTTP POST to a URL | OpenClaw, Slack, custom services |
| `exec` | Run a command (argv array, not shell) | NTM agent-mail, desktop notifications, anything |

**Configuration** (in `~/.config/ap/config.yaml` or per-session):

```yaml
signals:
  escalate:
    - type: webhook
      url: http://100.127.212.38:8080/api/signals   # OpenClaw
      headers:
        Authorization: "Bearer ${OPENCLAW_TOKEN}"
    - type: exec
      argv: ["ntm", "mail", "send", "${SESSION}", "--overseer", "Escalation: ${REASON}"]
  spawn:
    - type: stdout   # just log it, runner handles spawn internally
```

Or via CLI flags for one-off:

```bash
ap run ralph my-session \
  --on-escalate "webhook:http://openclaw:8080/api/signals"
```

**Graceful failure:** If a handler fails (webhook timeout, server down, exec error), it logs a warning to `events.jsonl` and falls through to the next handler. If ALL handlers fail, `stdout` always works as the backstop. A failed handler never crashes the session.

**Operational limits** (prevent runaway agents): `max_child_sessions` (10), `max_spawn_depth` (3), `handler_timeout` (30s). Configurable in `~/.config/ap/config.yaml`. Optional `max_concurrent_providers` (default: unlimited) for resource-constrained machines. When a limit is hit, the signal is logged as `signal.handler.error` in `events.jsonl` and the session continues (spawn is queued or skipped, handler is skipped).

### Signal Dispatch in the Runner

```
Agent writes status.json with agent_signals (and progress file — agent-owned, runner is read-only)
        │
        ▼
  Runner reads result + signals
        │
        ├── inject?     → prepend to next ${CONTEXT}, clear after
        ├── spawn?      → session.Start() (child session via Launcher), track in state
        └── escalate?   → pause session + dispatch to handler chain:
                              stdout (always)
                              → webhook (if configured) → OpenClaw, Slack, etc.
                              → exec (if configured) → ntm mail, notify-send, etc.
```

### How External Systems Respond (Ephemeral Callback Listener)

When the runner dispatches an escalation, it spins up a **ephemeral HTTP listener** to receive the response. No persistent server, no config, no port management:

1. Runner binds to `127.0.0.1:0` — OS assigns a free port (e.g., 41823)
2. Runner includes `callback_url` in the outbound webhook payload:
   ```json
   {
     "type": "escalate",
     "session": "auth",
     "iteration": 5,
     "message": "Two valid auth architectures. Need human decision.",
     "callback_url": "http://127.0.0.1:41823/resume"
   }
   ```
3. Runner blocks, waiting for a POST to `/resume` (with configurable timeout)
4. External system responds: `POST /resume {"context": "Go with JWT for v1"}`
5. Listener receives response → shuts down → runner resumes with injected context
6. If timeout fires first → listener shuts down → session pauses → falls through to manual `ap resume`

**The listener accepts one endpoint:**
```
POST /resume  {"context": "string"}  → 200 OK, session resumes
```

**Timeout behavior** — configurable per-escalation via the signal itself:
```json
{"type": "escalate", "message": "...", "timeout": "5m"}
```
- Default: `5m` for webhook escalations (automated systems)
- If no timeout specified and no webhook configured: no listener spawned, immediate pause (human-in-the-loop via `ap resume`)
- On timeout: `escalation.timeout` event → session pauses → manual `ap resume` still works

**Callback host** — the `callback_url` in outbound webhooks needs to be reachable by the receiving system:
```yaml
# In ~/.config/ap/config.yaml
signals:
  callback_host: "100.95.25.7"   # Tailscale IP for cross-machine callbacks
  # callback_host: "my-vps.tunnel.dev"  # Or a tunnel URL for internet-facing
```
- Default: `127.0.0.1` (works when orchestrator and runner are on the same machine — e.g., Claude Code launching `ap run`)
- For Tailscale peers (OpenClaw, other VPS): set to the machine's Tailscale IP
- For internet-facing services (Slack): requires a tunnel (ngrok, Cloudflare Tunnel, etc.) — the plan does NOT build tunnel support, just advertises whatever host you configure
- The ephemeral port is always appended automatically: `http://{callback_host}:{port}/resume`

**Graceful degradation:** The ephemeral listener is additive. All existing response paths still work:
```bash
# Human responds from terminal (always works, no listener needed)
ap resume auth --context "Go with JWT for v1"

# OpenClaw responds via callback_url (automatic, preferred)
# (OpenClaw POSTs to the callback_url from the webhook payload)

# NTM agent responds
ntm send my-session --cc "ap resume auth --context 'Go with JWT'"
```

**Callback authentication:** When `callback_host` is non-localhost, the runner generates a one-time token (crypto/rand, 32 bytes, hex-encoded) and includes it in the outbound webhook payload:
```json
{
  "callback_url": "http://100.95.25.7:41823/resume",
  "callback_token": "a1b2c3d4e5f6..."
}
```
The listener rejects any POST without a matching `Authorization: Bearer {callback_token}` header. When binding to `127.0.0.1`, no auth is required — anything on localhost already has shell access.

**Why ephemeral, not persistent:**
- Zero config — no port to choose, no process to manage
- Self-cleaning — if pipeline crashes, listener dies with it
- No security surface — listener exists only while waiting, localhost only
- Multiple pipelines — each escalation gets its own port, no routing needed

### State Model (Event-Sourced)

**`events.jsonl` is the single source of truth.** All state — including signal lifecycle — is derived from the event log. `state.json` is a compact snapshot/cursor, NOT a parallel data store.

**Write ordering for crash consistency:** append event line → `fsync` → write `state.json` atomically (temp + rename) → `fsync`. Event readers must tolerate partial trailing lines (truncated last line = incomplete write, ignore it). Snapshot regeneration from events must handle a corrupt/partial last event line by skipping it.

**`state.json`** — bounded snapshot, regenerated from events on startup. This is the **authoritative schema** — `ap status --json` returns this object directly.

```json
{
  "schema_version": 1,
  "session": "auth",
  "status": "running",
  "stage": "ralph",
  "node_id": "plan",
  "iteration": 5,
  "max_iterations": 25,
  "provider": "claude",
  "model": "opus",
  "started_at": "2026-02-27T10:03:32Z",
  "current_iteration_started_at": "2026-02-27T10:05:45Z",
  "parent_session": null,
  "child_sessions": [
    {"session": "auth-tests", "stage": "test-scanner", "status": "running", "iteration": 2, "max_iterations": 5}
  ],
  "recent_iterations": [
    {"iteration": 2, "duration_ms": 38000, "decision": "continue", "files_touched": ["src/auth/jwt.ts", "src/auth/middleware.ts"], "summary": "Added JWT validation", "signals": ["spawn:auth-tests"]},
    {"iteration": 3, "duration_ms": 41000, "decision": "continue", "files_touched": ["src/auth/jwt.ts"], "summary": "Fixed config mismatch", "signals": []},
    {"iteration": 4, "duration_ms": 52000, "decision": "continue", "files_touched": ["tests/auth.test.ts"], "summary": "Added test coverage", "signals": []}
  ],
  "files_touched": {
    "src/auth/jwt.ts": 3,
    "src/auth/middleware.ts": 1,
    "tests/auth.test.ts": 2
  },
  "signals": [
    {"type": "spawn", "iteration": 2, "status": "handled", "detail": "auth-tests"}
  ],
  "escalation": null,
  "last_event_offset": 2847
}
```

**Schema rules:**

| Field | Type | Bound | Description |
|-------|------|-------|-------------|
| `schema_version` | int | — | Always `1`. Enables future migrations. |
| `session` | string | — | Session name |
| `status` | enum | — | One of: `running`, `paused`, `completed`, `failed` |
| `stage` | string | — | Current stage name |
| `node_id` | string | — | Current pipeline node ID (equals `stage` for single-stage) |
| `iteration` | int | — | Current iteration number (1-based) |
| `max_iterations` | int | — | Maximum iterations for current stage |
| `provider` | string | — | Active provider name |
| `model` | string | — | Active model name |
| `started_at` | ISO 8601 | — | Session start time |
| `current_iteration_started_at` | ISO 8601 | — | When current iteration began (null if between iterations) |
| `parent_session` | string? | — | Parent session name, or null |
| `child_sessions` | array | max 10 | Active/recent children (bounded by `max_child_sessions`) |
| `recent_iterations` | array | **max 5** | Last 5 completed iterations. Oldest evicted on insert. |
| `files_touched` | object | **max 50** | Map of file path → modification count. Oldest evicted when full. |
| `signals` | array | **max 20** | Signal events for this session. Oldest evicted when full. |
| `escalation` | object? | — | Non-null when `status` is `paused`. Contains `reason`, `options`. |
| `last_event_offset` | int | — | Byte offset into events.jsonl for incremental replay |

**Bounded, not unbounded.** Arrays have explicit caps. The runner evicts oldest entries when bounds are hit. This keeps `state.json` O(1) in size regardless of session length. No `signal_ledger`. No reconciliation with events. The runner reads `last_event_offset` on startup and replays from there if needed.

**Signal lifecycle via events (replay safety):** Side-effecting signals use a two-phase event pattern. The **specific** event type (e.g., `signal.spawn`) replaces the generic completion event — there is no separate `signal.handled`.

1. **Before dispatch:** Append `signal.dispatching` event (with deterministic ID: `sig-{iteration}-{type}-{index}`, e.g., `sig-3-spawn-0`)
2. **Execute:** Perform the side effect (launch child process, send escalation webhook)
3. **After dispatch:** Append the **type-specific** result event (`signal.spawn`, `signal.spawn.failed`, `signal.escalate`, `signal.escalate.failed`)

On resume, the runner replays events:
- Sees `signal.dispatching` followed by a type-specific result event (`signal.spawn`, `signal.escalate`, etc.) → skip (already done)
- Sees `signal.dispatching` followed by a type-specific failure event (`signal.spawn.failed`, `signal.escalate.failed`) → skip (already attempted)
- Sees `signal.dispatching` with NO follow-up → re-dispatch (crash happened between steps 1 and 3)

This is the same safety guarantee as the old signal_ledger, but stored in the event log where it belongs. No mutable state file to corrupt.

**Canonical event sequences per signal type:**

| Signal | Events (in order) | Notes |
|--------|-------------------|-------|
| `inject` | `signal.inject` | Single event. Idempotent (overwrite, not accumulate). No two-phase needed. |
| `spawn` (success) | `signal.dispatching` → `signal.spawn` | Child launched via Launcher. `signal.spawn` includes child session name, PID. |
| `spawn` (failure) | `signal.dispatching` → `signal.spawn.failed` | Includes error message. Parent continues. |
| `escalate` (success) | `signal.dispatching` → `signal.escalate` | Session pauses. Includes reason, options. |
| `escalate` (handler failure) | `signal.dispatching` → `signal.escalate` + `signal.handler.error` | Escalation still pauses session even if handlers fail. Handler errors are non-fatal warnings. |

**Deterministic signal IDs:** Format is `sig-{iteration}-{type}-{index}` where `index` is the 0-based position within that signal type for that iteration (e.g., first spawn in iteration 3 = `sig-3-spawn-0`, second spawn = `sig-3-spawn-1`). This makes IDs predictable for replay matching.

- `schema_version: 1` enables future state format migrations without breaking existing sessions

### Event Extensions

New event types in `events.jsonl`:

```
session.started        — session launched successfully (emitted immediately on startup)
session.completed      — session finished (includes total iterations, duration, final status)
iteration.started      — iteration began (includes stage, node_id, iteration, provider, model)
iteration.completed    — iteration finished (includes stage, node_id, iteration, duration, decision, files_touched, summary)
iteration.failed       — iteration failed (includes stage, node_id, iteration, duration, exit_code, error, stderr_tail)
signal.inject          — context injected for next iteration (single event, no two-phase)
signal.dispatching     — side-effecting signal about to execute — written BEFORE the side effect
signal.spawn           — child session launched successfully (replaces generic "signal.handled" for spawn)
signal.spawn.failed    — child session failed to start (replaces generic "signal.failed" for spawn)
signal.escalate        — session paused for escalation (replaces generic "signal.handled" for escalate)
signal.escalate.failed — escalation handler chain failed (session still pauses; this is a warning)
signal.handler.error   — individual signal handler failed (warning, non-fatal; handler chain continues)
signal.handler.error   — signal handler failed (warning, non-fatal)
```

### What This Enables

The full signal protocol — inject, spawn, escalate — combined with the CLI gives agents a complete orchestration vocabulary: run loops, spawn children, pause for human decisions, resume with context. See **Success Criteria** (below) for the complete end-to-end examples showing each milestone's acceptance tests.

### Teaching Agents About Signals

Add to built-in prompts:

```markdown
## Orchestration Signals (Optional)

You can include `agent_signals` in your status.json to communicate with the orchestrator:

- **inject**: Pass critical context to the next iteration
  `"inject": "Important finding that the next iteration must know about"`

- **spawn**: Request a parallel child session (or pipeline) for independent work
  `"spawn": [{"run": "test-scanner", "session": "name", "context": "what to focus on", "n": 5}]`
  You can also chain stages: `"run": "test-scanner:5 -> elegance:3"`

- **escalate**: Request human review for decisions you shouldn't make alone
  `"escalate": {"type": "human", "reason": "Need architectural decision", "options": ["Option A", "Option B"]}`

Only use signals when genuinely useful. Most iterations just need `decision` and `summary`.
```

---

## Milestones

### Pre-M0: Structural Cleanup (1 day)
**Goal:** Code compiles, interfaces are unified, mock provider exists.

- [ ] P1: Unify Provider interface (delete `internal/provider` types, use `pkg/provider`)
- [ ] P2: Fix Claude provider to use `internal/exec.Run()`
- [ ] P3: Add `gopkg.in/yaml.v3` to go.mod
- [ ] P4: Rename `cmd/agent-pipelines/` → `cmd/ap/`
- [ ] P5: Extract `fileExists` to `internal/fsutil`
- [ ] P6: Implement `internal/mock` (MockProvider with canned responses)
- [ ] Freeze the `Provider` interface — this is the contract all agents build against

**Test:** `go build ./cmd/ap/` produces a binary. `go test ./...` passes (all existing + mock tests).

### M0a: Minimal Loop (2 days)
**Goal:** `ap run ./prompt.md -n 3 --fg my-session` runs 3 iterations end-to-end with Claude in foreground. No signals, no resume, no background — just the core loop. Robot mode output layer is wired from day one.

What to build:
- [ ] CLI argument parsing (`ap run <spec> <session>` with `-n`, `--provider`, `--model`)
- [ ] `internal/spec` — unified spec parser (stage name and file path detection for M0a; `stage:N` and typed AST in M0b; chain parsing and YAML compilation in M3)
- [ ] `internal/runner` — the iteration loop orchestrator (THE critical component)
  - Generate context → resolve prompt → execute provider → save output → check termination → update state → emit events
- [ ] Session directory creation (`.ap/runs/{session}/`)
- [ ] OS signal handling (SIGINT/SIGTERM → graceful shutdown → state update)
- [ ] **Robot mode output layer** (wire once, used everywhere):
  - [ ] `internal/output` — output mode detection (TTY auto-detect, `--json` flag, `AP_OUTPUT` env var)
  - [ ] Exit code constants (0/1/2/3/4/5/10/11/20)
  - [ ] Structured error format (`{code, message, detail, syntax, suggestions[], available_*}`)
  - [ ] `corrections[]` array in all success responses (empty when no fuzzy match fires)
  - [ ] No-args help (~100 tokens human, alias map in JSON)

What already exists that we wire up:
- `internal/exec` → process execution
- `internal/state` → session lifecycle
- `internal/context` → context.json generation
- `internal/resolve` → ${VAR} template substitution
- `internal/events` → event logging
- `internal/termination` → fixed iteration strategy
- `internal/validate` → security checks
- `internal/result` → output normalization
- `pkg/provider/claude` → Claude CLI provider (after P2 fix)

**Test:**
- Unit: `internal/runner` tested with mock provider (happy path, provider failure, fixed termination)
- Unit: `internal/output` — TTY detection, JSON formatting, exit code mapping, error structure validation
- Integration: `ap run scripts/stages/ralph/prompt.md -n 2 test-session` with mock provider
- Integration: `ap run bad-file.md test --json` returns structured error with SCREAMING_SNAKE code and suggestions
- Integration: `ap` (no args) returns help under 100 tokens (human) or alias map (JSON)
- E2E (optional): Same with real Claude CLI for 1 iteration

### M0b: Session Management + Stage Lookup (3 days)
**Goal:** `ap run elegance my-session` resolves built-in stages by name, launches in background, and returns immediately. `ap status` shows rich session state. `ap list`, `ap resume`, locking all work.

What to build:
- [ ] `internal/spec` — typed AST definitions (`StageSpec`, `ChainSpec`, `FileSpec`); implement `StageSpec` and `FileSpec` parsing (chain parsing deferred to M3)
- [ ] Stage name resolution via `internal/stage` (wired into spec parser)
- [ ] `stage:N` syntax parsing (e.g., `ralph:25`)
- [ ] `internal/session` — session launcher API (delegates to Launcher for process creation, shared by CLI `ap run` and spawn signal)
- [ ] Internal runner subcommand: `ap _run --session <name> --request <path>` — deterministic entrypoint that bypasses fuzzy parsing, spec resolution, and human UX. Parent writes `.ap/runs/<session>/run_request.json` atomically before launch. Child reads request and runs exactly that pipeline. Used by all Launchers, `ap resume`, spawn signal, and future remote orchestration.
- [ ] `Launcher` interface with `TmuxLauncher` (default) and `ProcessLauncher` (fallback) — both invoke `ap _run`, never `ap run`
- [ ] Background execution as default — `ap run` uses Launcher, returns immediately
- [ ] `--fg` flag for foreground mode (CI/testing)
- [ ] Per-launcher readiness mechanism — TmuxLauncher uses `tmux wait-for` channels, ProcessLauncher uses `os.Pipe()` with `cmd.ExtraFiles` (no file polling)
- [ ] `internal/lock` — flock-based session locking with stale PID detection
- [ ] Resume logic — `ap run` on existing session exits with error (never prompts); `ap resume` for continuation
- [ ] `ap resume` command — resume paused/crashed sessions with optional `--context`
- [ ] `ap list` (scan stages directory, print name + description; `--json` for machine output)
- [ ] `ap status` — reads `state.json` snapshot (O(1), no event scanning):
  - Current stage, iteration, provider/model
  - Per-iteration activity log (last 5: duration, decision, files modified)
  - Child session status
  - Aggregate files touched with modification counts
  - Signal history
  - `--json` returns snapshot directly
- [ ] `state.json` snapshot — updated atomically (write-to-temp + rename) by runner after each event append
- [ ] `ap kill` (kill session via Launcher + SIGTERM to process + release lock + mark state failed)
- [ ] `ap logs` (tail events.jsonl; `-f` for follow; `--json` for structured output)
- [ ] `ap clean` (remove session dir + lock + Launcher session; `--all` for bulk cleanup; safety: only clean `completed`/`failed` sessions, refuse `paused`/`running` unless `--force`)
- [ ] Global `--json` flag — every command supports machine-parseable JSON output (wired via `internal/output` from M0a)
- [ ] **Fuzzy command matching** (`internal/fuzzy`):
  - [ ] Command synonym map (`start`→`run`, `ls`→`list`, `stop`→`kill`, etc.)
  - [ ] Levenshtein matching on command names and stage names (distance threshold: 2 for 5+ chars, 1 for ≤4 chars)
  - [ ] Flag alias map (`--iterations`→`-n`, `--foreground`→`--fg`, `--provider anthropic`→`--provider claude`, etc.)
  - [ ] Argument order recovery (swap spec/session when first arg doesn't resolve as spec but second does)
  - [ ] Spec syntax recovery (`ralph 25`→`ralph:25`, directory path→stage name; chain recovery like `ralph > elegance`→`"ralph -> elegance"` deferred to M3)
  - [ ] Stage name recovery (strip prefixes, strip extensions, Levenshtein, keyword-to-category)
  - [ ] **Safety: no Levenshtein and no argument-swap recovery for `kill`, `clean`, and `clean --all`.** Only exact synonym map for destructive commands. `clean --all` requires `--force` in both human and JSON mode.
  - [ ] All corrections collected and returned in `corrections[]` array on success responses
- [ ] **AGENTS.md** — CLI contract documentation for AI agents (~100 lines, shipped in project root)
- [ ] `ap kill` idempotent — killing a dead session returns exit 0 with `"was_running": false`
- [ ] `ap clean` idempotent — cleaning a clean session returns exit 0
- [ ] `session.started` event — emitted immediately on runner startup
- [ ] `session.completed` event — emitted when session finishes (total iterations, duration, final status)
- [ ] `iteration.started` event — emitted when each iteration begins
- [ ] `iteration.completed` event — emitted when each iteration finishes (duration, decision, files, summary)

**Test:**
- Unit: Lock acquisition/release, stale PID detection, resume cursor
- Unit: Spec parser — stage name, stage:N, file path detection (yaml, prompt), error cases (chain parsing tested in M3)
- Unit: Launcher interface — TmuxLauncher, ProcessLauncher
- Integration: Full run → kill → resume cycle with mock provider
- Integration: `ap run ralph test` returns exit 0 and per-Launcher readiness confirms startup
- Integration: `ap run bad-spec test` returns exit 2 with specific parse error
- Integration: `ap status test --json` returns valid JSON matching state.json snapshot schema
- Integration: `state.json` snapshot updates atomically after each iteration
- `ap list --json` returns valid JSON array
- `ap logs test --json` returns valid JSONL with `iteration.completed` events
- All commands: `--json` produces valid JSON output
- Fuzzy: `ap start ralph auth` resolves to `ap run ralph auth` with correction hint
- Fuzzy: `ap run ralf auth` resolves stage to `ralph` with correction hint
- Fuzzy: `ap run ralph auth --iterations 25` normalizes to `-n 25` with hint
- Fuzzy: `ap run auth ralph` swaps argument order with hint
- Fuzzy: `ap run ralph 25 auth` normalizes to `ralph:25` with hint
- Fuzzy: `ap xyz auth` returns `UNKNOWN_COMMAND` error with all 7 commands listed
- Fuzzy: `ap run nonexistent auth` returns `STAGE_NOT_FOUND` with available stages and suggestions
- Fuzzy: `ap kill auth` on dead session returns exit 0 (idempotent)
- AGENTS.md exists and is under 100 lines

### M0c: Agent Signals (2 days)
**Goal:** The 3 agent signals are parsed, validated, dispatched, and tracked via event-sourced lifecycle. Resume doesn't duplicate side effects.

What to build:
- [ ] `internal/signals` — parse `agent_signals` from status.json, validate, dispatch
  - `inject` (prepend to next `${CONTEXT}`, clear after consumption)
  - `spawn` → parse `run` field with spec parser, call `session.Start()` (delegates to Launcher, separate process)
  - `escalate` → always pause session + print to stdout (overrides `decision`)
  - `checkpoint` and `budget` → log warning ("signal reserved for future version"), skip
  - No configurable handlers yet — hardcoded stdout behavior only
- [ ] Two-phase signal lifecycle via events — `signal.dispatching` → execute → type-specific result (`signal.spawn`/`signal.escalate`) or failure (`signal.spawn.failed`/`signal.escalate.failed`)
- [ ] Event replay on resume — detect incomplete dispatches and re-execute
- [ ] Operational limits enforcement: `max_child_sessions` (10), `max_spawn_depth` (3)
- [ ] Parent/child session tracking via events
- [ ] Extend `internal/result` with `AgentSignals` field

**Test:**
- Unit: `internal/signals` tested independently (parse, validate, dispatch, event lifecycle)
- Integration: Resume after crash doesn't re-fire completed signals (`signal.dispatching` followed by `signal.spawn` or `signal.escalate`)
- Integration: Resume DOES re-fire `signal.dispatching` with no type-specific follow-up event (crash mid-dispatch)
- Integration: Spawn respects `max_child_sessions` limit
- Integration: Spawn uses `session.Start()` which delegates to the Launcher (separate process)

### M1: Judgment Termination (1 day)
**Goal:** Judgment-based loops work. `ap run elegance my-session` runs until consensus.

What to build:
- [ ] `internal/judge` — invoke Haiku to evaluate iteration history
  - Consensus tracking: N consecutive "stop" decisions (default N=2)
  - Min iterations before checking (from stage.yaml `termination.min_iterations`)
  - Judge failure fallback (3 consecutive failures → fall back to fixed iteration count using stage max_iterations, log warning; pipeline keeps running)
  - Retry logic (up to 2 attempts per invocation)
- [ ] Wire judgment termination into `internal/runner` iteration loop
- [ ] Read termination config from `stage.yaml` (already parsed by `internal/stage`)

**Test:** Run with mock provider + mock judge. Verify:
- Stops after 2 consecutive "stop" decisions
- Respects min_iterations
- Handles judge failures gracefully
- Falls back to fixed iteration on repeated judge errors

### M2: Codex Provider + Signal Handlers (1-2 days)
**Goal:** `ap run ralph --provider codex my-session` works. Signal handlers are configurable.

What to build:
- [ ] Codex CLI provider (shell out to `codex exec` with correct flags)
  - **Research confirmed (2026-02-27):** `codex exec` (v0.106.0) exits cleanly on completion with exit code 0. No watchdog needed — same lifecycle as Claude provider (run, wait, collect output).
  - The Bash engine's `_run_codex_with_watchdog` (~100 LOC, 5 known bugs, zero tests) is NOT needed. It was a workaround for interactive Codex; `codex exec` is non-interactive by design.
  - Recommended flags: `--dangerously-bypass-approvals-and-sandbox --ephemeral` (skip session persistence to prevent disk buildup in pipelines)
  - Useful Codex-specific flags to wire up:
    - `--json` — JSONL event stream for programmatic output parsing
    - `-o <file>` — write last agent message to file (easy output capture)
    - `--output-schema <file>` — enforce structured output via JSON Schema (could enforce status.json format)
  - Default model is now `gpt-5.3-codex` (was `gpt-5.2-codex`). Default reasoning effort is `xhigh` — override to `medium` or `high` for multi-iteration loops (xhigh cost adds up fast)
  - Prompt piped via stdin (`printf '%s' "$prompt" | codex exec -`)
  - Safety timeout via Go `context.WithTimeout` (default 300s) — standard process management, not a watchdog
- [ ] Provider selection via `--provider` flag
- [ ] Model resolution (codex model aliases, including `gpt-5.3-codex` as new default)
- [ ] Pluggable signal handler chain (config in `~/.config/ap/config.yaml`)
  - `webhook` handler (HTTP POST with JSON payload, with retry/timeout)
  - `exec` handler (run command with argv array + signal vars)
  - Graceful failure: log warning, fall through to next handler, stdout always works
- [ ] `--on-escalate` CLI flag for one-off webhook/exec configuration

**Test:** Same prompt runs with both `--provider claude` and `--provider codex`. Codex exits cleanly with code 0. Webhook handler delivers escalation to a test HTTP server. Handler failure falls through gracefully.

### M3: Multi-Stage Pipelines (2 days)
**Goal:** `ap run "improve-plan:5 -> refine-tasks:5" my-session` and `ap run refine.yaml my-session` both work. This completes the core product — everything is a pipeline.

What to build:
- [ ] Chain expression parser in spec resolver (`"a:5 -> b:3"` → Pipeline object with sequential nodes)
- [ ] `internal/compile` — YAML parser for pipeline.yaml → Pipeline object
- [ ] Multi-stage orchestration in `internal/runner` (sequential node execution)
- [ ] Stage-to-stage input passing (chain uses default `inputs.from: previous`; YAML supports explicit `inputs.from`, `inputs.select`)

**Test:**
- `ap run "improve-plan:5 -> refine-tasks:5" test` with mock provider — outputs flow between stages
- `ap run refine.yaml test` produces identical behavior
- Spawn signal with `"run": "a:5 -> b:3"` creates multi-stage child session

### M4: Parallel Blocks (1 day)
**Goal:** Parallel provider execution works. Completes full feature parity with the Bash engine.

What to build:
- [ ] `internal/parallel` — concurrent provider execution with isolated contexts
- [ ] Manifest aggregation for downstream stages
- [ ] `from_parallel` input resolution

**Test:** Pipeline with `parallel: providers: [claude, codex]` runs both concurrently.

---

## Test Strategy

### Unit Tests
Every package has `*_test.go`. The critical new package (`internal/runner`) gets the most coverage.

### Integration Tests
- **Mock loop tests:** Full iteration loop with mock provider returning canned responses
- **Golden file tests:** Known stage.yaml → verify context.json output matches snapshot

### E2E Tests (CI-optional)
- Spawn real Claude CLI for 1 iteration, verify output

### Commands
```bash
go test ./...                          # All unit tests
go test ./internal/runner/... -v       # Runner tests (most important)
go test -run TestIntegration ./...     # Integration only
AP_E2E=1 go test -run TestE2E ./...   # End-to-end (requires API keys)
```

### Coverage Targets
- `internal/runner`: >80% (this is the critical path)
- `internal/judge`: >80%
- `internal/lock`: >80%
- CLI commands: >60%
- Providers: integration tests only

---

## Implementation Approach

### Option A: Single Agent, Sequential (Recommended for M0a-M2)

The simplicity reviewer's argument is persuasive: M0a-M2 is fundamentally sequential work. The runner depends on providers, judgment depends on the runner, Codex depends on the provider interface. One agent builds M0a → M0b → M0c → M1 → M2. Total: ~11 days (see revised estimates below), zero coordination overhead.

**Revised timeline (post-premortem):** Every original estimate was 2-3x under. Realistic:
| Milestone | Original | Revised | Why |
|-----------|----------|---------|-----|
| Pre-M0 | 0.5 day | 1 day | Provider interface unification is the riskiest pre-work |
| M0a | 1 day | 2 days | Foreground loop + template resolution + event-sourced state |
| M0b | 1-2 days | 3 days | Most ambitious milestone — session mgmt, 7 CLI commands, readiness, spec parser |
| M0c | 1 day | 2 days | Signal dispatch, spawn safety, event lifecycle, re-dispatch idempotency |
| M1 | 0.5-1 day | 1 day | Judgment termination is well-scoped |
| M2 | 0.5-1 day | 1-2 days | Research resolved: codex exec exits cleanly, no watchdog. Signal handlers. |
| M3 | 1-2 days | 2 days | Multi-stage pipeline compiler |
| M4 | 1 day | 1 day | Parallel blocks |
| **Total** | **~5 days** | **~14 days** | |

### Option B: Two Agents, Staggered

If we want some parallelism:

| Agent | Work | When |
|-------|------|------|
| Agent A | Pre-M0 cleanup + M0a (minimal loop) + M0b (session mgmt) | Day 1-6 |
| Agent B | M0c (signals) + M1 (judgment) + M2 (Codex provider) | Day 6-11 (starts after M0b interfaces freeze) |

One handoff point. Agent B starts when Agent A finishes the runner and provider interface.

### Option C: Four-Agent Swarm (M3+M4)

When we reach multi-stage pipelines and parallel blocks, a 4-agent swarm makes sense:
- Agent 1: Pipeline compiler
- Agent 2: Multi-stage runner orchestration
- Agent 3: Parallel block executor
- Agent 4: Integration tests for all of the above

Staff up for this after M0-M2 prove the foundation works.

---

## Prompt Contract (Must Be Identical to Bash Engine)

No migration strategy — the Go engine is a clean replacement, not a gradual migration. But all 37 existing prompts read `context.json`, write `status.json`, and reference template variables. These contracts must be identical or every prompt breaks:

- `${CTX}`, `${PROGRESS}`, `${STATUS}` template variable paths and semantics
- `context.json` structure (agents read this — any change breaks all 37 prompts)
- Session directory layout: `.ap/runs/{session}/iterations/NNN/`
- `status.json` schema that agents write (decision, reason, summary, work, errors, agent_signals)
- Beads cleanup on session crash/kill: the **runner** releases `in_progress` beads labeled `pipeline/{session}` back to `open` (this is runner responsibility, not prompt responsibility — a crashed prompt can't clean up after itself)

---

## What's Cut (and Why)

| Cut | Why |
|-----|-----|
| `library.yaml` | `stage.yaml` already exists. One config format. |
| `{{.param}}` template syntax | YAGNI. Zero existing prompts use params. Add later as `${VAR}` extensions if needed. |
| `--set` flag | Cut with param system. |
| `--inline` flag | Write a temp file, pass as spec. |
| `--termination`, `--command` flags | Read from stage.yaml. Override via env vars. |
| `ap library render/show` | Read the .md file directly. |
| `ap compile`, `ap validate` | YAML validation happens automatically in `ap run`. No separate command needed. |
| ~~`ap kill`, `ap logs`, `ap clean`~~ | **Restored in Rev 5.** Agents need a self-contained interface — they shouldn't need to know tmux naming conventions or filesystem layout. |
| Backward compat (`ap compat run`) | Only user is us. Old Bash engine keeps working. |
| Hooks (`internal/hooks`) | No existing prompts use them. Add when needed. |
| `checkpoint` signal (v1) | Git provides rollback. When added: metadata-first manifests, not directory copies. |
| `budget` signal (v1) | Display-only is pointless. When added: real policy enforcement (max_cost, spawn caps). |
| `file` signal handler | `exec` with `tee` covers this. Not worth a dedicated implementation. |

---

## Decisions (Closed)

1. **Binary name:** `ap`. Directory rename included in Pre-M0 P4.
2. **Prompt embed:** `go:embed` for built-in stages, local `scripts/stages/` overrides at runtime. Both.
3. **Queue termination:** Keep `bd` (beads) integration. Ralph's prompt handles this via `bd ready`. **However, beads crash cleanup IS the runner's responsibility** — on session crash/kill, the runner releases `in_progress` beads labeled `pipeline/{session}` back to `open`. The prompt can't clean up after itself if it crashed.
4. **Agent-mail integration:** Not v1. Agents coordinate via the filesystem (session directories, state.json) and agent-mail if NTM is managing the tmux session.

---

## Success Criteria

**M0a is done when:**
```bash
# M0a runs in foreground (--fg) since background/tmux is M0b
$ ap run test-prompt.md -n 3 --fg test-session
[iteration 1/3] Running claude... done (45s) → continue
[iteration 2/3] Running claude... done (38s) → continue
[iteration 3/3] Running claude... done (41s) → continue
Session test-session completed: 3/3 iterations

# state.json + events.jsonl exist and are valid (ap status is M0b)
$ cat .ap/runs/test-session/state.json | jq '.status'
"completed"
```

**M0c signals work when:**
```bash
$ ap run ralph:25 auth
Session auth started (ralph, 25 iterations, claude/opus)
Monitor: ap status auth

$ ap status auth          # check on it
Session: auth (paused)
Stage:   ralph (iteration 5/25)
⚠ Escalation: "Two valid auth architectures. Need human decision."
Resume: ap resume auth --context "your decision"

Recent activity:
  [iter 1] 45s → continue | 3 files modified
  [iter 2] 38s → continue | spawned auth-tests
  [iter 3] 41s → continue | injected context
  [iter 4] 52s → continue

Children:
  auth-tests (test-scanner, iter 3/5, running)

$ ap resume auth --context "Go with JWT for v1"
Session auth resumed from iteration 6
Monitor: ap status auth

$ ap status auth          # later
Session: auth (completed)
Iterations: 6/25 (stopped early — agent: "all tasks complete")
Children: auth-tests (completed, 3/5)
```

**M1 is done when:**
```bash
$ ap run elegance my-session
Session my-session started (elegance, 10 iterations, claude/opus)
Monitor: ap status my-session

$ ap status my-session    # later
Session: my-session (completed)
Iterations: 5/10 (judgment: 2 consecutive stops)
Duration: 4m 8s

Recent activity:
  [iter 1] 52s → continue
  [iter 2] 48s → continue
  [iter 3] 55s → continue (judge: continue, confidence: 0.7)
  [iter 4] 43s → stop (judge: stop, confidence: 0.9)
  [iter 5] 50s → stop (judge: stop, confidence: 0.95)
```

**M2 is done when:**
```bash
# Codex provider works
$ ap run ralph:5 codex-test --provider codex
[iter 1/5] codex... done (30s) → continue
...

# Escalation reaches OpenClaw via webhook
$ ap run ralph auth --on-escalate "webhook:http://openclaw:8080/api/signals"
[iter 3/25] claude... done → PAUSED
  ⚠ Escalation dispatched to webhook (http://openclaw:8080/api/signals)
  ⚠ Also printed to stdout (fallback always active)
```

**M3 is done when:**
```bash
# Ad-hoc chain
$ ap run "improve-plan:5 -> refine-tasks:5" refine-session
Session refine-session started (2-stage chain, claude/opus)
Monitor: ap status refine-session

$ ap status refine-session  # later
Session: refine-session (running)
Pipeline: 2-stage chain (improve-plan:5 -> refine-tasks:5)
Stage:   refine-tasks (2/2) — iteration 3/5
Previous: improve-plan (completed, 5/5)

Recent activity (refine-tasks):
  [iter 1] 40s → continue (reading improve-plan output)
  [iter 2] 38s → continue
  [iter 3] running for 22s...

$ ap status refine-session  # later
Session: refine-session (completed)
Pipeline: 2 stages, 10 total iterations
Duration: 7m 23s

# Same thing via YAML
$ ap run refine.yaml refine-session-2
Session refine-session-2 started (refine.yaml, claude/opus)
Monitor: ap status refine-session-2

$ ap status refine-session-2
[stage 1/2: improve-plan]
  ...
```

That's the whole product. Everything else is iteration.

---

## Rev 10 Additions (2026-03-03)

The following four features were identified during Agent Relay design review and incorporated into the plan. All fit cleanly into existing or new milestones.

### Addition 1: Live Message Bus (`internal/messages`)

**What:** Agents can communicate mid-iteration via a shared append-only JSONL file per session (`.ap/runs/{session}/messages.jsonl`). The runner tails this file for structured messages. Agents in parallel blocks can read each other's messages in real-time.

**Why:** Currently agents can only communicate when an iteration completes (via `status.json` signals). The message bus enables live, mid-iteration communication — e.g., agent 1 in a parallel block discovers a critical finding and agent 2 can read it immediately without waiting for both iterations to finish.

**Schema:**
```json
{"timestamp": "...", "from": "claude", "type": "finding", "content": "JWT library has CVE-2026-1234"}
{"timestamp": "...", "from": "codex", "type": "ack", "ref": "...", "content": "Switching to jose library"}
```

**Where it fits:** New section after "Agent Signals Protocol." New package `internal/messages`. Wired into context.json as `paths.messages`. Agents get `${MESSAGES}` template variable pointing to the file.

**Milestone:** M4 (parallel blocks) — message bus only matters when multiple agents run concurrently. Added as a sub-task of parallel block implementation.

### Addition 2: `ap watch` Command

**What:** Reactive event subscriptions. Tail `events.jsonl` and execute commands when specific events fire.

```bash
ap watch auth --on completed "notify-send 'Pipeline done'"
ap watch auth --on escalate "curl -X POST http://openclaw:8080/api/signals"
ap watch auth --on idle "bd assign-next pipeline/auth"
```

**Also configurable in `~/.config/ap/config.yaml`:**
```yaml
hooks:
  on_completed: "notify-send 'Pipeline ${SESSION} done'"
  on_escalate: "curl -X POST http://openclaw:8080/api/signals"
```

**Where it fits:** New CLI command after `ap logs`. New package `internal/watch` — thin wrapper that tails events.jsonl, pattern-matches event types, and exec's the configured command.

**Milestone:** M5 (new). Not blocking anything else. Low effort (~0.5 day) since `ap logs -f` already does event tailing — `ap watch` adds filtering + exec dispatch.

### Addition 3: Race Termination Strategy

**What:** Spawn N agents on the same problem concurrently, take the first one that writes `decision: stop` with a successful result, kill the rest.

```yaml
# In stage.yaml
termination:
  type: race
  agents: 3        # spawn 3 concurrent agents
  accept: first    # take first successful stop
```

**Implementation:** A parallel block variant where instead of waiting for all providers to complete, the runner monitors all agents' status files and terminates the block when the first agent writes a successful result. Remaining agents are killed via process group signals.

**Where it fits:** `internal/termination/race.go`. Depends on `internal/parallel` (M4) since it needs concurrent execution infrastructure.

**Milestone:** M5 (alongside `ap watch`). Low effort (~0.5 day) since it builds on parallel block infrastructure.

### Addition 4: Auto-Retry with Backoff

**What:** Instead of the current "fail fast, resume smart" behavior, stages can optionally configure automatic retry on provider failure.

```yaml
# In stage.yaml (optional — default is still fail-fast)
retry:
  max_attempts: 3       # retry up to 3 times per iteration
  backoff: 5s           # wait between retries (doubles each attempt: 5s, 10s, 20s)
  on_exhausted: pause   # what to do when all retries fail: pause | abort (default: abort)
```

**Behavior:** When a provider execution fails (non-zero exit, timeout, no status.json), the runner retries up to `max_attempts` times with exponential backoff. Each retry emits an `iteration.retried` event. If all retries are exhausted, the `on_exhausted` policy kicks in — `abort` (current behavior, fail the session) or `pause` (pause for investigation, resumable via `ap resume`).

**Where it fits:** `internal/runner` iteration loop. Small addition to the retry path that currently calls `mark_failed` immediately.

**Milestone:** M2 (alongside Codex provider + signal handlers). Codex is more likely to have transient failures (API timeouts, rate limits), so retry is most useful when Codex ships. ~0.5 day additional effort.

### Updated Milestone Summary (Rev 10)

| Milestone | Goal | Effort | Changes from Rev 9 |
|-----------|------|--------|-------------------|
| Pre-M0 | Structural cleanup (unify interfaces, mock provider) | 1 day | No change |
| M0a | Minimal loop (foreground, fixed termination, robot mode output) | 2 days | No change |
| M0b | Session mgmt (background, status, list, resume, fuzzy CLI, locking) | 3 days | No change |
| M0c | Agent signals (inject/spawn/escalate, event lifecycle) | 2 days | No change |
| M1 | Judgment termination (Haiku judge, consensus tracking) | 1 day | No change |
| M2 | Codex provider + signal handlers + **auto-retry** | 2 days | +0.5 day for retry logic |
| M3 | Multi-stage pipelines (chain parser, YAML compiler, stage-to-stage flow) | 2 days | No change |
| M4 | Parallel blocks + **live message bus** | 1.5 days | +0.5 day for message bus |
| **M5** | **`ap watch` + race termination** | **1 day** | **NEW milestone** |
| **Total** | | **~15.5 days** | **+1.5 days from Rev 9's ~14 days** |
