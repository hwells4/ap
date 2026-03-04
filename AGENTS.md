# AGENTS.md

Constraints for autonomous agents working in this repository.

## Execution Envelope

- **Read first, write second.** Start every task in read-only analysis. Understand the affected packages, their tests, and their callers before editing.
- **Writable paths**: Only files inside this repository. Do not modify `~` or system paths.
- **Network**: Assume network access is off. No remote resources unless the task explicitly requires it.
- **Dependencies**: Do not add dependencies to `go.mod` without explicit approval. The project aims for near-zero deps (only `gopkg.in/yaml.v3`).

## Definition of Done

Work is complete only when:

1. `go build ./...` succeeds
2. `go test ./...` passes
3. `go vet ./...` reports no issues
4. New code has corresponding tests (TDD: test first, then implement)
5. No `TODO` or `FIXME` left without a tracking bead

## Proof Command

```bash
go build ./... && go test ./... && go vet ./...
```

Run this before claiming any task is complete.

## Required Workflow

Every task must produce:

1. **Short plan** — affected packages, approach, edge cases considered
2. **Tests first** — write failing tests before implementation
3. **Implementation** — make the tests pass
4. **Verification** — run the proof command, report results

## Code Conventions

### Naming

- Packages: lowercase single word (`runner`, `signals`, `spec`)
- Files: `snake_case.go` with `_test.go` suffix for tests
- Interfaces: verb-noun (`Provider`, `Strategy`, `Handler`)
- Errors: wrap with package context: `fmt.Errorf("runner: start session: %w", err)`

### Architecture Rules

- `internal/` packages are NOT importable outside this module
- `pkg/` packages define the public SDK (Provider interface, types)
- Process execution MUST go through `internal/exec.Run()` — never raw `os/exec`
- Template substitution MUST use `internal/resolve` — one syntax (`${VAR}`) everywhere
- Event writing MUST use `internal/events` — append-only, flock-protected
- State transitions MUST use `internal/state` — crash-recoverable lifecycle

### Testing Rules

- MockProvider for unit tests (configurable canned responses)
- FakeProviderBin for integration tests (real process boundary)
- Table-driven tests for pure logic
- `TestIntegration_*` prefix for integration tests
- `TestE2E_*` prefix for E2E tests (gated behind `AP_E2E=1`)
- Race detector: all concurrent code tested with `go test -race`

### What NOT To Do

- Do not bypass `internal/exec` for process management
- Do not add Go template syntax (`{{.Var}}`) — we use `${VAR}` only
- Do not read stdout/stderr for agent decisions — decisions come from `status.json`
- Do not create new provider interfaces — `pkg/provider.Provider` is frozen
- Do not add dependencies without approval
- Do not modify contracts (Provider interface, template variables, event schema) without explicit discussion

## Milestone Dependencies

Tasks are labeled by milestone. Respect the dependency chain:

```
Pre-M0 → M0a → M0b → M0c → M1 → M2 → M3 → M4 → M5
```

Do not work on a milestone before its dependencies are complete. Use `bd ready` to find unblocked tasks.

## Key Documents

| Document | Purpose |
|----------|---------|
| `docs/plans/2026-02-27-refined-go-rewrite-plan.md` | Full architecture, contracts, milestone specs |
| `docs/plans/test-strategy.md` | Test infrastructure, layers, CI lanes |
| `CLAUDE.md` | Project overview, package map, quick reference |

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
