# ap CLI — Full Contract Reference

This is the detailed reference for the ap agent pipelines CLI.
For quick-start usage, see the main SKILL.md.

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
| `judge.verdict` | Judgment termination evaluation |
| `judge.fallback` | Judge fell back to fixed termination |
| `signal.dispatching` | Signal dispatch started |
| `signal.inject` | Inject signal processed |
| `signal.escalate` | Escalation dispatched |
| `signal.spawn` | Child session spawned |
| `signal.spawn.failed` | Child spawn failed |
| `signal.handler.error` | Handler error (non-fatal) |
| `hook.completed` | Hook executed successfully |
| `hook.failed` | Hook failure (non-fatal) |
| `error` | General error |

## Signal Handlers

Configured in `~/.config/ap/config.yaml` under `signal_handlers`:

### stdout (default)
Prints JSON payload to stdout.

### webhook
```yaml
signal_handlers:
  - type: webhook
    url: https://example.com/hook
    headers:
      Authorization: "Bearer token"
```
HTTP POST with JSON payload. Includes `callback_url`/`callback_token` when callback listener is active.

### exec
```yaml
signal_handlers:
  - type: exec
    argv: ["notify-send", "ap: ${SESSION} escalated: ${REASON}"]
```
Template vars: `${SESSION}`, `${STAGE}`, `${ITERATION}`, `${REASON}`, `${CHILD_SESSION}`, `${TYPE}`.

## Callback Listener

When configured, an ephemeral HTTP server listens for `POST /resume` responses. The `callback_url` and `callback_token` are included in webhook payloads for human-in-the-loop workflows. Non-localhost binds auto-generate bearer tokens.

## Spawn Signal Details

```json
{
  "signals": {
    "spawn": [
      {
        "run": "stage:5",
        "session": "child-name",
        "project_root": "/optional/path",
        "n": 10
      }
    ]
  }
}
```

Subject to `max_child_sessions` and `max_spawn_depth` limits.

## Judgment Termination Details

- Judge model evaluates after each iteration
- Needs `consensus` consecutive `"stop"` verdicts
- Falls back to fixed-iteration mode after 3 consecutive judge failures
- Fallback uses original `runs:` count as the cap
- Set `runs:` conservatively when using judgment termination

## Work Manifest

Each iteration captures a git diff telemetry snapshot:

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

## Output Contract

- JSON mode: `--json`, non-TTY stdout, or `AP_OUTPUT=json`
- Success: exit 0 with payload plus `corrections[]`
- Errors: non-zero exit with `error.code`, `error.message`, `error.detail`, `error.syntax`, `error.suggestions[]`
- Optional `error.available_*` metadata for recovery

## Forgiving Syntax Details

- Command synonyms: `start→run`, `ls→list`, `stop→kill`
- Typo correction: Levenshtein distance (<=2 for long, <=1 for short)
- Flag alias normalization: `--iterations→-n`, `--provider anthropic→--provider claude`
- Argument order recovery: misplaced `<spec> <session>` recovered
- Safety rule: `kill` and `clean` do NOT use fuzzy matching

## Internal Launcher

- Entrypoint: `ap _run --session <name> --request <path> [--resume]`
- Process mutex: `flock` on `.ap/locks/{session}.lock`
- `run_request.json` persisted for crash recovery
- `ap resume` cleans orphaned iterations before re-launching

## SQLite Tables

`sessions`, `iterations`, `outputs`, `events`, `locks`, `session_children`, `schema_version`
