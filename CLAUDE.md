# CLAUDE.md

This file provides guidance to Claude Code when working in this repository.

## What This Is

`ap` is a Go CLI that orchestrates autonomous AI agent pipelines. It runs multi-iteration agent workflows in tmux sessions with any AI coding agent (Claude Code, Codex, Gemini CLI, or any CLI tool). Each iteration spawns a fresh agent instance that reads accumulated progress to maintain context without degradation.

This is a **ground-up rewrite** of the Bash-based [agent-pipelines](https://github.com/hwells4/agent-pipelines) system. The Bash version works but is unreliable. This repo is the Go replacement.

**Core philosophy:**
- Fresh agent per iteration prevents context degradation
- Two-agent consensus prevents premature stopping
- Planning tokens are cheaper than implementation tokens
- Everything is a pipeline — a "loop" is just a single-stage pipeline

## Quick Reference

```bash
# Build
go build ./cmd/ap

# Run all tests
go test ./...

# Run tests with race detector
go test -race ./...

# Run specific package tests
go test ./internal/runner/...
go test ./internal/signals/...

# Run integration tests only
go test -run TestIntegration ./...

# Verify everything compiles
go build ./...
```

## Architecture

```
ap run ralph my-session          # stage name → 1-stage pipeline
ap run ralph:25 my-session       # stage with iteration count
ap run "a:5 -> b:5" my-session   # chain → multi-stage pipeline
ap run refine.yaml my-session    # YAML → full pipeline definition
ap run ./custom.md my-session    # prompt file → 1-stage pipeline
```

### Package Map

**Production-ready (tested, stable):**

| Package | Purpose |
|---------|---------|
| `internal/exec` | Process execution with signal cascade, bounded I/O, process groups |
| `internal/events` | Append-only events.jsonl with flock |
| `internal/state` | Session lifecycle + crash recovery with `ResumeFrom()` |
| `internal/context` | v3 context.json generation for each iteration |
| `internal/resolve` | `${VAR}` template substitution |
| `internal/result` | Agent output normalization from status.json |
| `internal/termination` | Termination strategies (fixed iteration count) |
| `internal/validate` | Security validation checks |
| `internal/stage` | Stage definition resolution from stage.yaml |
| `internal/engine` | Provider registration (thin wrapper) |
| `pkg/provider` | SDK types — Provider interface, ExecuteRequest/ExecuteResult |
| `pkg/provider/claude` | Claude CLI provider implementation |

**To be built:**

| Package | Purpose | Milestone |
|---------|---------|-----------|
| `internal/runner` | Iteration loop orchestrator — the core engine | M0a |
| `internal/spec` | Unified spec parser (stage name, chain, YAML, prompt) | M0a |
| `internal/signals` | Agent signal parsing, dispatch, handler chain | M0c |
| `internal/session` | Session launcher (delegates to tmux/process) | M0b |
| `internal/judge` | Judgment provider invocation for consensus | M1 |
| `internal/compile` | YAML pipeline compiler | M3 |
| `internal/parallel` | Parallel provider execution | M4 |
| `internal/mock` | MockProvider for tests | Pre-M0 |
| `internal/testutil` | Test infrastructure (FakeProviderBin, Clock, etc.) | Pre-M0 |
| `internal/fsutil` | Shared file utilities | Pre-M0 |
| `internal/messages` | Live message bus for watch | M4 |

### Data Directory

```
.ap/                              # Project-local session data (gitignored)
├── runs/{session}/               # Session run data
│   ├── run_request.json          # Durable request record
│   ├── state.json                # Lifecycle state + crash recovery
│   ├── events.jsonl              # Append-only event log
│   └── stage-NN-{name}/         # Per-stage data
│       └── iterations/{NNN}/    # Per-iteration data
├── locks/{session}.lock          # Session locks
├── stages/                       # User stage overrides
└── pipelines/                    # User pipeline overrides

~/.config/ap/                     # Global config (XDG-compliant)
├── config.yaml                   # Signal handlers, limits, defaults
└── prompts/                      # Global prompt overrides
```

## Key Contracts

These are frozen interfaces that all code must respect. See `docs/plans/2026-02-27-refined-go-rewrite-plan.md` for full details.

### Contract 1: Provider Interface

```go
type ExecuteRequest struct {
    Prompt     string            // Fully resolved prompt (all ${VAR} substituted)
    WorkingDir string            // Project root (where .ap/ lives)
    Timeout    time.Duration     // Default 300s
    Model      string            // Resolved model name, empty = provider default
    EnvVars    map[string]string // Additional env vars for the provider process
    InputFiles []string          // Paths to input files
}

type ExecuteResult struct {
    ExitCode   int
    Stdout     string            // Capped at 1MB by internal/exec
    Stderr     string            // Capped at 1MB by internal/exec
    Duration   time.Duration
    StatusJSON *AgentStatus      // Parsed from status.json, or nil
}
```

Environment variables set for every provider invocation: `AP_AGENT=1`, `AP_SESSION`, `AP_STAGE`, `AP_ITERATION`.

### Contract 2: Template Variables

The ONLY template variables (all use `${VAR}` syntax, substituted in prompt.md only):

| Variable | Value |
|----------|-------|
| `${CTX}` | Path to context.json |
| `${PROGRESS}` | Path to progress-{session}.md |
| `${STATUS}` | Path where agent writes status.json |
| `${ITERATION}` | 1-based iteration number |
| `${SESSION_NAME}` | Session name |
| `${CONTEXT}` | Injected context text (from --context, env, or inject signal) |
| `${OUTPUT}` | Path to write output file (if configured) |

### Contract 3: Event Schema

All events have: `type` (string), `timestamp` (ISO 8601), `session` (string).

Core event types: `session.started`, `session.completed`, `session.failed`, `iteration.started`, `iteration.completed`, `iteration.failed`, `signal.dispatching`, `signal.dispatched`, `stage.started`, `stage.completed`.

### Contract 4: Termination Precedence

1. `escalate` signal → session pauses immediately
2. Provider execution failure → retry policy decides
3. Agent decision `error` → stop stage
4. Agent decision `stop` → stop stage early
5. Judgment consensus → stop stage/pipeline
6. Fixed iteration count reached → stop

## Development Rules

### Testing (TDD — no exceptions)

- Write the test first, make it pass, refactor
- MockProvider is the foundation — every feature starts with a mock test
- Fast and deterministic by default — no real Claude/Codex/tmux in default test path
- Race detector required for concurrent code: `go test -race`
- Test the contract, not the implementation — assert on observable behavior
- Test layers: 60% unit, 30% integration, 10% E2E

### Code Style

- Zero dependencies beyond `gopkg.in/yaml.v3` — stdlib otherwise
- One template syntax: `${VAR}` everywhere, no Go templates
- Process execution always through `internal/exec.Run()` — never raw `os/exec`
- All file I/O uses `os.WriteFile` atomic patterns or `internal/events` append
- Errors are wrapped with `fmt.Errorf("package: context: %w", err)`

### Milestone Order

Work proceeds through milestones sequentially. Each milestone produces something runnable.

| Milestone | What it delivers |
|-----------|-----------------|
| Pre-M0 | Structural cleanup: unified Provider, mock, yaml dep, rename cmd/ap |
| M0a | Minimal `ap run ralph my-session` — single-stage loop that works |
| M0b | Session management: `ap logs`, `ap clean`, crash recovery, telemetry |
| M0c | Agent signals: inject, spawn, escalate with two-phase lifecycle |
| M1 | Judgment termination (two-agent consensus) |
| M2 | Multi-provider (Codex), config, signal handlers, backoff, escalation |
| M3 | Multi-stage pipelines, chain expressions, stage-to-stage data flow |
| M4 | Parallel blocks, message bus |
| M5 | Watch command, race termination |

## Stage Library

Stage definitions live in `stages/` (copied from agent-pipelines). Each stage has:
- `stage.yaml` — configuration (provider, iterations, completion strategy)
- `prompt.md` — the prompt template with `${VAR}` placeholders

Pipeline definitions live in `pipelines/` as YAML files that chain stages together.

## Beads (Task Tracking)

This project uses `bd` (beads) for task tracking. Beads are organized by milestone labels.

```bash
bd list                           # List all beads
bd list --label go-rewrite        # Go rewrite beads only
bd ready                          # Show beads ready to work on
bd show <id>                      # Full bead details
bd claim <id>                     # Claim a bead
bd done <id>                      # Mark complete
```
