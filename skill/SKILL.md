---
name: ap
description: This skill should be used when running, building, monitoring, or authoring autonomous agent pipelines with the ap CLI. It covers operating sessions (start, monitor, resume, kill), authoring stages and pipelines, writing ap-result decision blocks, using signals (inject, escalate, spawn), and configuring termination strategies and lifecycle hooks.
---

<auto_trigger>
- "run a pipeline"
- "start a pipeline"
- "run a stage"
- "start a stage"
- "create a stage"
- "build a pipeline"
- "author a stage"
- "ap run"
- "ap status"
- "ap list"
- "pipeline session"
- "agent pipeline"
- "check session status"
- "resume session"
- "kill session"
- "what stages are available"
- "ap-result"
- "spawn child session"
- "escalate"
- "termination strategy"
</auto_trigger>

# ap — Agent Pipelines CLI

Machine-first CLI for running autonomous agent pipelines. Sessions run in tmux, state lives in SQLite. Agents receive a `context.json` each iteration and emit decisions via `ap-result` blocks.

## Quick Start

```bash
ap list --json                              # list available stages
ap run ralph:10 my-session --json           # run 10 iterations
ap status my-session --json                 # check state
ap logs my-session -f --json                # follow live
ap kill my-session --json                   # terminate
```

---

## 1. Operating Sessions

### Spec Syntax

| Format | Example | Meaning |
|--------|---------|---------|
| Stage | `ralph` | Run stage, 1 iteration |
| Stage + count | `ralph:10` | Run stage, 10 iterations |
| Chain | `"improve-plan:5 -> refine-tasks:5"` | Sequential multi-stage |
| Pipeline file | `./pipeline.yaml` | YAML pipeline definition |
| Prompt file | `./prompt.md` | Ad-hoc prompt as stage |

Forgiving syntax: `ralph 25` recovers to `ralph:25`, `a > b` recovers to `a -> b`.

### Commands

```bash
# Start
ap run <spec> <session> --json
ap run <spec> <session> --provider claude -m opus --json
ap run <spec> <session> --context "extra instructions" --json
ap run <spec> <session> --explain-spec --json    # dry-run

# Monitor
ap status <session> --json
ap logs <session> -f --json
ap watch --json

# Query store
ap query sessions --status running --json
ap query iterations --session NAME --json
ap query events --session NAME --type signal.escalate --json
ap query events --session NAME --after 5 --json   # poll new events

# Resume / Kill / Clean
ap resume <session> --json
ap resume <session> --context "new instructions" --json
ap kill <session> --json
ap clean <session> --json
ap clean --all --json

# Cross-project
ap status <session> --project-root /path/to/project --json
```

### Session States

```
running -> paused | completed | failed | aborted
paused  -> running | aborted
failed  -> running | aborted
completed, aborted = terminal
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 2 | Bad arguments |
| 3 | Not found |
| 4 | Already exists |
| 5 | Locked |
| 10 | Provider error |
| 11 | Timeout |
| 20 | Paused (escalation) |

---

## 2. Being an Agent Inside ap

Each iteration, the runner provides a `context.json`. The agent reads it, does work, and emits an `ap-result` block.

### Context File

The agent receives `${CTX}` pointing to `context.json`:

```json
{
  "session": "my-session",
  "pipeline": "pipeline-name",
  "stage": {"id": "improve-plan", "index": 0, "template": "improve-plan"},
  "iteration": 3,
  "paths": {
    "session_dir": ".ap/runs/my-session",
    "progress": ".ap/runs/my-session/progress.md",
    "history": ".ap/runs/my-session/stage-00-improve-plan/iterations/003/history.md",
    "output": ".ap/runs/my-session/stage-00-improve-plan/iterations/003/output.md",
    "messages": ".ap/runs/my-session/messages.jsonl"
  },
  "inputs": {
    "from_stage": {"plan-stage": ["/path/to/output.md"]},
    "from_previous_iterations": ["/path/iteration-001/output.md"],
    "from_initial": ["/path/input1.md"]
  },
  "limits": {"max_iterations": 25, "remaining_seconds": 3600}
}
```

### Template Variables

Available in prompts and `output_path`:

| Variable | Description |
|----------|-------------|
| `${CTX}` | Path to context.json |
| `${SESSION}` | Session name |
| `${ITERATION}` | Current iteration (1-based) |
| `${INDEX}` | Zero-based iteration index |
| `${PROGRESS}` | Path to progress.md |
| `${HISTORY}` | Path to history.md |
| `${OUTPUT}` | Path to output.md |
| `${OUTPUT_PATH}` | Resolved output_path from stage config |
| `${CONTEXT}` | Injected context (from signal.inject or --context) |
| `${MESSAGES}` | Path to messages.jsonl |

### Emitting Decisions (ap-result)

Agents emit decisions via a fenced code block in stdout:

````
```ap-result
{
  "decision": "continue",
  "summary": "Implemented feature X"
}
```
````

**Valid decisions**: `continue`, `stop`, `error`

**Extraction rules** (first match wins):
1. Last `` ```ap-result `` fenced block parsed as JSON
2. Non-zero exit code → `"error"`
3. Default → `"continue"` with last 200 chars as summary

### Signals

Signals go inside ap-result to trigger runner-side actions:

```json
{
  "decision": "continue",
  "summary": "Need human review",
  "signals": {
    "inject": "context string for next iteration",
    "escalate": {"type": "human", "reason": "Need design review"},
    "spawn": [{"run": "review:3", "session": "child-review"}],
    "warnings": ["Non-critical note"]
  }
}
```

| Signal | Effect |
|--------|--------|
| `inject` | String becomes `${CONTEXT}` in next iteration |
| `escalate` | Pauses session, dispatches to handler chain |
| `spawn` | Launches child sessions |
| `warnings` | Logged as warnings (informational) |

### Progress & History Files

| File | Written By | Purpose |
|------|-----------|---------|
| `progress.md` | Agent | Working notes that survive stage transitions |
| `history.md` | Runner | Read-only iteration summary rebuilt from SQLite |

---

## 3. Authoring Stages

A stage is a directory with `stage.yaml` + `prompt.md`:

```
my-stage/
  stage.yaml
  prompt.md
```

### stage.yaml

```yaml
name: my-stage
description: What this stage does

termination:
  type: fixed          # or: judgment

# For judgment termination:
# termination:
#   type: judgment
#   min_iterations: 2
#   consensus: 2

delay: 3               # seconds between iterations

output_path: "docs/${SESSION}-output.md"   # optional custom output location

hooks:                  # optional, overrides global/pipeline hooks
  post_iteration: 'git add -A && git diff --cached --quiet || git commit -m "$AP_SUMMARY"'
```

### prompt.md

```markdown
# My Agent

Context: ${CTX}
Progress: ${PROGRESS}

${CONTEXT}

## Instructions

1. Read ${PROGRESS} for prior work
2. Read inputs: `jq -r '.inputs.from_initial[]' ${CTX} | xargs cat`
3. Do the work
4. Update ${PROGRESS} with notes
5. Emit decision:

` `` ap-result
{"decision": "continue", "summary": "What was done"}
` ``
```

### Termination Strategies

| Type | Behavior |
|------|----------|
| `fixed` | Stop after N iterations (N from spec, e.g. `ralph:10`). Default: 1. Immediate stop on `"stop"` or `"error"` decision. |
| `judgment` | Judge model evaluates after each iteration. Needs `consensus` consecutive `"stop"` verdicts. Falls back to fixed after 3 judge failures. |

### Stage Resolution Precedence

1. Project-local: `.ap/stages/{name}/stage.yaml`
2. Pipeline-relative: `{pipeline_dir}/stages/{name}/stage.yaml`
3. `$AGENT_PIPELINES_ROOT/scripts/stages/{name}/stage.yaml`
4. User-global: `~/.config/ap/stages/{name}/stage.yaml`
5. Built-in (embedded in binary)

---

## 4. Authoring Pipelines

Pipelines chain multiple stages together. Two ways:

### Chain Spec (inline)

```bash
ap run "improve-plan:5 -> refine-tasks:5" my-feature --json
```

### Pipeline YAML File

```yaml
name: refine
description: Plan then implement

hooks:
  pre_session: "git checkout -b ap/${SESSION}"
  post_session: "git push -u origin HEAD && gh pr create --fill"

nodes:
  - id: improve-plan
    stage: improve-plan
    runs: 5

  - id: refine-tasks
    stage: refine-tasks
    runs: 3
    inputs:
      from: improve-plan
      select: latest
```

Run with: `ap run ./pipeline.yaml my-session --json`

**Node fields:**
- `id`: unique identifier for this node
- `stage`: which stage template to use
- `runs`: number of iterations
- `inputs.from`: ID of upstream node whose output feeds into this node
- `inputs.select`: `latest` (default) or `all`

---

## 5. Lifecycle Hooks

Shell commands at key lifecycle points. Non-fatal: failures emit events but don't stop the session.

| Hook | When |
|------|------|
| `pre_session` | Before first iteration |
| `pre_iteration` | Before each iteration |
| `pre_stage` | Before a pipeline stage begins |
| `post_iteration` | After each iteration |
| `post_stage` | After a pipeline stage completes |
| `post_session` | After session completes successfully |
| `on_failure` | When session fails |

**Precedence** (most-specific wins, no merging):
1. Stage hooks (stage.yaml) — single-stage runs only
2. Pipeline hooks (pipeline.yaml)
3. Global hooks (~/.config/ap/config.yaml)

**Environment variables**: `$AP_SESSION`, `$AP_STAGE`, `$AP_ITERATION`, `$AP_STATUS`, `$AP_SUMMARY`, `$AP_CONTEXT`

**Template variables in commands**: `${SESSION}`, `${STAGE}`, `${ITERATION}`, `${STATUS}`, `${SUMMARY}`, `${CONTEXT}`

```yaml
# ~/.config/ap/config.yaml
hooks:
  pre_session: "git checkout -b ap/${SESSION} 2>/dev/null || git checkout ap/${SESSION}"
  post_iteration: 'git add -A && git diff --cached --quiet || git commit -m "$AP_SUMMARY"'
  post_session: "git push -u origin HEAD"
  timeout: 30s
```

---

## 6. Configuration

### Global Config

`~/.config/ap/config.yaml`:

```yaml
hooks:
  pre_session: "git checkout -b ap/${SESSION}"
  post_iteration: 'git add -A && git diff --cached --quiet || git commit -m "$AP_SUMMARY"'

limits:
  iteration_timeout: 30m    # 0 = no timeout

retry:
  max_attempts: 1           # total attempts per iteration
  backoff: 5s               # doubles per attempt
  on_exhausted: abort       # or "pause"

signal_handlers:
  - type: stdout            # print to stdout (default)
  - type: webhook
    url: https://example.com/hook
    headers:
      Authorization: "Bearer token"
  - type: exec
    argv: ["notify-send", "ap: ${SESSION} escalated"]
```

### Retry

| Field | Default | Description |
|-------|---------|-------------|
| `max_attempts` | 1 | Total attempts per iteration |
| `backoff` | 5s | Initial backoff, doubles each attempt |
| `on_exhausted` | abort | `abort` fails session, `pause` pauses for investigation |

---

## 7. Common Workflows

### Poll until done

```bash
ap run ralph:5 my-task --json
while true; do
  STATUS=$(ap status my-task --json 2>/dev/null | jq -r '.status')
  case "$STATUS" in
    completed|failed|aborted) break ;;
    paused) ap resume my-task --json ;;
  esac
  sleep 10
done
```

### Spawn child sessions from agent

Inside an ap-result block:
```json
{
  "decision": "continue",
  "summary": "Spawning review",
  "signals": {
    "spawn": [{"run": "peer-code-reviewer:1", "session": "review-auth"}]
  }
}
```

### Escalate to human

```json
{
  "decision": "continue",
  "summary": "Need approval",
  "signals": {
    "escalate": {"type": "human", "reason": "Design review needed", "options": ["approve", "reject"]}
  }
}
```

Session pauses (exit 20). Resume with: `ap resume <session> --context "approved" --json`

### Pass context between iterations

```json
{
  "decision": "continue",
  "summary": "Found 3 bugs",
  "signals": {
    "inject": "Focus on auth module next - 3 bugs found in login flow"
  }
}
```

Next iteration receives this as `${CONTEXT}`.

### Check what's running

```bash
ap query sessions --status running --json | jq '.sessions[] | {name, stage: .current_stage}'
```

---

## 8. Storage & Artifacts

```
.ap/
  ap.db                          # SQLite (WAL mode) — source of truth
  runs/{session}/
    run_request.json             # launch config (crash recovery)
    state.json                   # state snapshot
    progress.md                  # agent working notes
    messages.jsonl               # message bus
    stage-00-{id}/
      iterations/
        001/
          context.json           # what agent received
          output.md              # what agent produced
          history.md             # runner-generated summary

~/.local/state/ap/control.db    # machine-wide session index
~/.config/ap/config.yaml        # global configuration
~/.config/ap/stages/            # user-global stages
```

---

## Guidelines

- Always use `--json` for machine-readable output
- Use `--explain-spec` to validate before launching
- Exit code 20 means paused — check for escalation, use `ap resume`
- `ap kill` is idempotent — safe to call multiple times
- Decision authority is the SQLite store, not files on disk
- Use `progress.md` for agent working notes that persist across iterations
- Use `history.md` (read-only) to review what happened in prior iterations
- For detailed contract reference, see [references/contract.md](references/contract.md)
