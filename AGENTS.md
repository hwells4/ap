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
- Safety rule: `kill` and `clean` do **not** use typo-based fuzzy matching (exact synonyms only).

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

## Spec Types
- Stage spec: `ralph`
- Stage with count: `ralph:25`
- Chain spec: `"improve-plan:5 -> refine-tasks:5"`
- YAML pipeline file: `./pipeline.yaml`
- Prompt file: `./prompt.md`

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
