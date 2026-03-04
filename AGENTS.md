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
| Check session snapshot | `ap status <session> [--json]` |
| Resume paused/failed session | `ap resume <session> [--context "..."] [--json]` |
| Terminate a session | `ap kill <session> [--json]` |
| Read session events | `ap logs <session> [-f] [--json]` |
| Clean run artifacts | `ap clean <session>|--all [--force] [--json]` |

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
5. Escalation/paused handling:
   - if paused (`exit 20`/state paused), gather context and `ap resume <session> --json`
6. Cleanup/termination:
   - `ap kill <session> --json` (idempotent)
   - `ap clean <session>|--all --json`

## Spec Types
- Stage spec: `ralph`
- Stage with count: `ralph:25`
- Chain spec: `"improve-plan:5 -> refine-tasks:5"`
- YAML pipeline file: `./pipeline.yaml`
- Prompt file: `./prompt.md`

## Agent Notes
- Internal launcher entrypoint is `ap _run --session <name> --request <path>`.
- Decision authority is `status.json` (not stdout/stderr).
- Preserve and inspect `corrections[]` for deterministic machine workflows.
