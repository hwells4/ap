# ap

A machine-first CLI for running autonomous agent pipelines in tmux/process sessions.

`ap` coordinates AI agents through iterative stage-based workflows, with full observability via a SQLite event store. Designed for both human operators and agent-to-agent orchestration.

## Install

```bash
go install github.com/hwells4/ap/cmd/ap@latest
```

Or build from source:

```bash
go build -o ap ./cmd/ap
```

## Quick Start

```bash
# Run a bug-hunt + security-audit loop, repeated 3 times
ap run "(bug-hunt:3 -> security-audit:1) x3" audit-session

# List available stages
ap list

# Run a stage
ap run ralph my-session

# Run with fixed iteration count
ap run ralph:25 my-session

# Check status
ap status my-session

# Tail logs
ap logs my-session -f

# Kill a session
ap kill my-session

# Clean up artifacts
ap clean my-session
```

## Commands

### run

Start a new pipeline session.

```bash
ap run <spec> <session> [flags]
```

| Flag | Description |
|------|-------------|
| `-n, --iterations N` | Override iteration count |
| `--provider NAME` | Provider: `claude` (default), `codex` |
| `-m, --model MODEL` | Model identifier |
| `--on-escalate HANDLER` | Signal handler for escalations |
| `--project-root DIR` | Project root directory |
| `-i, --input FILE` | Input files (repeatable) |
| `-c, --context TEXT` | Context string for the agent |
| `-f, --force` | Force relaunch if session exists |
| `--fg, --foreground` | Run in foreground and wait |
| `--explain-spec` | Parse and return spec without launching |
| `--json` | JSON output |

```bash
ap run ralph my-session
ap run ralph:25 my-session --provider codex
ap run "improve-plan:5 -> refine-tasks:5" chain-session
ap run ./prompt.md prompt-session --fg
ap run ralph my-session --explain-spec --json
```

### list

Discover available stages.

```bash
ap list [--json]
```

Shows builtin stages and project-local stages from `.claude/stages/`.

### status

Check session state.

```bash
ap status <session> [--project-root DIR] [--json]
```

### resume

Resume a paused or failed session.

```bash
ap resume <session> [--context TEXT] [--project-root DIR] [--json]
```

Automatically cleans orphaned iterations from crashes before resuming.

### kill

Terminate a session. Idempotent and cascades to child sessions.

```bash
ap kill <session> [--project-root DIR] [--json]
```

### logs

Read session events.

```bash
ap logs <session> [-f] [--project-root DIR] [--json]
```

Use `-f` to follow in real time.

### clean

Remove run artifacts.

```bash
ap clean <session> [--force] [--project-root DIR] [--json]
ap clean --all [--force] [--json]
```

Skips running/paused sessions unless `--force` is set.

### watch

Watch for events and trigger shell hooks.

```bash
ap watch <session> --on <event> <command> [--json]
```

Event shorthands: `completed`, `escalate`, `failed`, `idle`. Variable expansion in commands: `${SESSION}`, `${EVENT}`, `${REASON}`, `${ITERATION}`.

```bash
ap watch my-session --on completed "notify-send 'Done: ${SESSION}'"
ap watch my-session --on escalate "echo '${REASON}'" --on failed "echo 'fail'"
```

### query

Query sessions, iterations, and events.

```bash
ap query sessions [--status STATUS] [--scope project|instance] [--json]
ap query iterations --session NAME [--stage NAME] [--json]
ap query events --session NAME [--type TYPE] [--after SEQ] [--json]
```

## Spec Types

The `<spec>` argument determines what to run, in order of parsing precedence:

| Type | Example | Description |
|------|---------|-------------|
| Repeat | `"(a:5 -> b:3) x3"` | Repeat a chain N times |
| Chain | `"a:5 -> b:5"` | Sequential multi-stage pipeline |
| YAML file | `./pipeline.yaml` | Pipeline definition file |
| Prompt file | `./prompt.md` | Direct prompt for the agent |
| Stage with count | `ralph:25` | Named stage with iteration override |
| Bare stage | `ralph` | Named stage with default iterations |

**Repeat syntax**: `(chain) xN` expands the chain N times. Accepts `x`, `X`, or `*` as the multiplier. `(a:3 -> b:1) x3` becomes `a:3 -> b:1 -> a:3 -> b:1 -> a:3 -> b:1`.

**Spec recovery**: `ralph 25` is recovered to `ralph:25`. Chain separators ` > ` and `,` are normalized to ` -> `.

## Stages

Stages are reusable units of work containing a prompt template and configuration.

**Builtin stages** are embedded in the binary. **Project stages** live at `.claude/stages/{name}/`:

```
.claude/stages/my-stage/
  stage.yaml      # config (description, iterations, etc.)
  prompt.md       # prompt template
```

Project stages override builtins with the same name. Use `ap list` to see all available stages.

### Stage Configuration

```yaml
name: my-stage
description: "What this stage does"
prompt: "custom-prompt.md"    # default: prompt.md (relative to stage dir)

termination:
  type: fixed                 # fixed | judgment | race
  iterations: 10              # fixed: stop after N (default: 1)
  consensus: 2                # judgment: consecutive stops needed (default: 2)
  min_iterations: 3           # judgment: minimum before stop allowed (default: 3)

delay: 3                      # seconds between iterations
output_path: "out/${SESSION}-${ITERATION}.md"  # custom output file path
```

## Termination Strategies

| Type | Config | Description | Status |
|------|--------|-------------|--------|
| `fixed` | `iterations: N` | Stop after N iterations (default: 1). Immediate stop on `"stop"` or `"error"` decision. | Tested |
| `judgment` | `consensus: 2, min_iterations: 3` | Judge model evaluates after each iteration. Needs N consecutive `"stop"` verdicts. Falls back to fixed after 3 judge failures. | Tested |
| `race` | `agents: N, accept: first` | N concurrent providers run in parallel. First successful result wins. Requires `parallel` block. | Not wired — see below |

> **Note**: `race` termination and `parallel` blocks have unit-tested components (`internal/parallel/`, `internal/termination/race.go`, `internal/context/from_parallel`) but the runner does not yet support parallel nodes (`runner.go:959` rejects them). These features are architectural scaffolding — do not use in production pipelines.

## Pipelines

Multi-stage workflows defined in YAML:

```yaml
name: my-pipeline
nodes:
  - id: plan
    stage: improve-plan
    runs: 5

  - id: refine
    stage: refine-tasks
    runs: 3
    inputs:
      from: plan              # read outputs from the "plan" node
      select: latest          # "latest" (default) or "all"
      from_initial: true      # include CLI --input files
```

## Session History & Progress

Two files track session state across iterations:

- **`progress.md`** — Session-scoped working notes written by the agent. Survives stage transitions.
- **`history.md`** — Runner-generated iteration summary rebuilt from SQLite before each iteration. Read-only for agents.

## Providers

| Name | Aliases | Description |
|------|---------|-------------|
| `claude` | `anthropic` | Claude API (default) |
| `codex` | - | Codex CLI |

## Configuration

File: `~/.config/ap/config.yaml`

```yaml
defaults:
  launcher: tmux       # tmux | process
  provider: claude     # claude | codex
  model: ""            # empty = provider decides

signals:
  callback_host: 127.0.0.1
  handler_timeout: 30s
  escalate:
    - type: webhook
      url: https://example.com/escalate
    - type: exec
      argv: ["notify-send", "Escalation"]
  spawn:
    - type: stdout

limits:
  max_child_sessions: 10
  max_spawn_depth: 3
  max_concurrent_providers: 0   # 0 = unlimited

hooks:
  on_completed: "echo done"
  on_escalate: "echo escalated"
  on_idle: "echo idle"
```

**Precedence** (for launcher, provider, model): CLI flag > `AP_*` env var > config file > compiled default.

**Environment variables**: `AP_PROVIDER`, `AP_MODEL`, `AP_LAUNCHER`, `AP_OUTPUT`.

## Storage Model

All session state lives in a SQLite database at `.ap/ap.db` (WAL mode).

| Table | Purpose |
|-------|---------|
| `sessions` | Session metadata, status, config |
| `iterations` | Per-iteration decision, summary, exit code, signals |
| `outputs` | stdout/stderr/context paired 1:1 with iterations |
| `events` | Append-only audit log, monotonic `seq` per session |
| `locks` | Process-level session locking |
| `session_children` | Parent-child session relationships |

**Decision authority is the SQLite store**, not stdout/stderr or files on disk.

### Session States

```
pending -> running -> completed
                   -> failed -> running (resume)
                   -> paused -> running (resume)
                   -> aborted
```

### Cross-Project Lookup

A machine-wide control plane at `~/.local/state/ap/control.db` indexes sessions across projects. When a session name isn't found locally, `ap` queries the control plane. If the name is ambiguous across projects, an error with `--project-root` suggestions is returned.

## Forgiving Syntax

`ap` recovers from common mistakes and reports corrections in output.

**Command synonyms**: `start`/`exec`/`launch` -> `run`, `ls` -> `list`, `stop`/`abort` -> `kill`, `tail` -> `logs`, `rm`/`prune` -> `clean`.

**Typo correction**: Levenshtein distance matching for commands and stage names. Destructive commands (`kill`, `clean`) only accept exact synonyms -- no fuzzy matching.

**Spec recovery**: `ralph 25` -> `ralph:25`, swapped `<session> <spec>` -> `<spec> <session>`, chain arrows ` > ` and `,` -> ` -> `.

**Provider aliases**: `anthropic` -> `claude`.

## Output Contract

**Mode detection** (in order): `--json` flag, non-TTY stdout, `AP_OUTPUT=json` env var, otherwise human.

**JSON success**: payload with `corrections[]` array (always present).

**JSON error**:

```json
{
  "error": {
    "code": "STAGE_NOT_FOUND",
    "message": "...",
    "detail": "...",
    "syntax": "ap run <spec> <session> [flags]",
    "suggestions": ["ap list"]
  }
}
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 2 | Invalid arguments |
| 3 | Not found |
| 4 | Already exists |
| 5 | Locked |
| 10 | Provider error |
| 11 | Provider timeout |
| 20 | Session paused |

## Architecture

```
cmd/ap/             CLI commands and arg parsing
internal/
  compile/          YAML pipeline compiler -> Pipeline object
  config/           YAML configuration (~/.config/ap/config.yaml)
  context/          Context manifest (context.json) generation
  controlplane/     Machine-wide session index
  engine/           Iteration engine (provider interaction loop)
  exec/             Process execution, signal cascade, bounded I/O
  extract/          Agent output extraction (ap-result parsing)
  fuzzy/            Typo correction and command synonyms
  judge/            Judgment termination with consensus tracking
  lock/             Process-level session locking (flock)
  messages/         Live message bus (JSONL per session)
  mock/             MockProvider for testing
  output/           JSON/human output formatting
  parallel/         Concurrent provider execution (race strategy)
  resolve/          ${VAR} template substitution
  runner/           Session runner orchestration, retry, signal dispatch
  session/          Launcher backends (tmux, process)
  signals/          Signal parsing and validation
  spec/             Spec parsing (stage, chain, file)
  stage/            Stage resolution (builtin + project)
  store/            SQLite session/iteration/event store
  termination/      Termination strategies (fixed, judgment, race)
  testutil/         Test utilities and fake provider binaries
  validate/         Input validation and security checks
pkg/
  provider/         Provider interface and registry
  provider/claude/  Claude provider
  provider/codex/   Codex provider
```

For the full agent contract (context.json schema, template variables, signal formats, event types, retry config), see [AGENTS.md](AGENTS.md).

## Testing

```bash
go test ./...
```
