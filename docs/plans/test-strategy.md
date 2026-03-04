# Unified Test Strategy for `ap` Go Rewrite

> Synthesized from Claude Opus 4.6 and Codex GPT-5.2 proposals. This document guides all TDD development on the `ap` CLI.

---

## 1. Philosophy

- **TDD**: Write the test first, make it pass, refactor. No exceptions.
- **MockProvider is the foundation**: Every feature starts with a test using MockProvider. If you can't test it with MockProvider, you haven't defined the interface yet.
- **Fast and deterministic by default**: No real Claude/Codex/tmux in the default test path. CI must pass without network access or external binaries.
- **Race detector required**: All concurrent code runs under `go test -race` in CI.
- **Test the contract, not the implementation**: Assert on observable behavior (exit codes, file contents, event sequences), not on internal state.

## 2. Test Layers (60/30/10)

| Layer | Share | What | Where | Naming | Runs |
|-------|-------|------|-------|--------|------|
| Unit | 60% | Pure logic, no I/O, table-driven | `*_test.go` next to source | `TestFoo`, `TestBar` | Every PR |
| Integration | 30% | Real filesystem, process boundaries, event replay, crash recovery | `*_test.go` next to source | `TestIntegration_*` | Every PR |
| E2E | 10% | Real `ap` binary, full session lifecycle | `e2e/` directory | `TestE2E_*` | `AP_E2E=1` gated |

**Why 60/30/10 (not 70/20/10):** `ap` is a process-orchestrating CLI. Most real bugs live at process boundaries — stdin piping, exit codes, file locking, crash recovery. The extra 10% in integration tests catches these before they reach E2E.

## 3. Key Test Infrastructure

### 3.1 `pkg/provider.MockProvider`

In-memory mock that returns deterministic `status.json` responses. Configurable:

- Canned responses per iteration (success, failure, error)
- Configurable delays (for timeout testing)
- Configurable failure modes (exit code, signal, no status.json)
- Records all invocations for assertion

```go
mock := provider.NewMockProvider(
    provider.WithResponses([]provider.Response{
        {Decision: "continue", Summary: "did work"},
        {Decision: "stop", Summary: "done"},
    }),
)
```

**This is day-1 infrastructure.** Built before any feature code.

### 3.2 `internal/testutil.FakeProviderBin`

Compiles a tiny Go binary at test time that mimics the real provider CLI contract:

- Reads prompt from stdin
- Reads env vars (`AP_SESSION`, `AP_STAGE`, etc.)
- Writes `status.json` to the path from env/args
- Exits with configurable exit code
- Captures stdout/stderr behavior

```go
bin := testutil.FakeProviderBin(t, testutil.FakeBehavior{
    StatusResponse: `{"decision":"continue","summary":"ok"}`,
    ExitCode:       0,
    Delay:          100 * time.Millisecond,
})
// bin.Path is the compiled binary path, usable as a provider command
```

**Why both MockProvider and FakeProviderBin?** MockProvider tests logic (fast, in-memory). FakeProviderBin tests the process boundary (stdin piping, env propagation, exit codes, signal handling). Both are needed because most integration bugs live at the process boundary.

### 3.3 `internal/testutil.Clock`

Interface for time injection. All time-dependent code (runner, retry, backoff) accepts a `Clock`:

```go
type Clock interface {
    Now() time.Time
    Since(time.Time) time.Duration
    NewTimer(time.Duration) Timer
    Sleep(time.Duration)
}
```

Tests use a fake clock for deterministic timing. Production uses `realClock{}`.

### 3.4 `internal/testutil.IDGen`

Deterministic ID generator for signals and events:

```go
gen := testutil.NewIDGen("test")
// Returns "test-001", "test-002", ...
```

Ensures replay tests produce identical event sequences regardless of execution timing.

### 3.5 `internal/testutil.TempSession`

Helper that creates a `.ap/runs/{session}/` tree in `t.TempDir()`:

```go
sess := testutil.NewTempSession(t, "my-test",
    testutil.WithIterations(3),                    // pre-populate 3 completed iterations
    testutil.WithState(state.Running),             // set state.json
    testutil.WithEvents([]event.Event{...}),       // pre-populate events.jsonl
)
// sess.Dir is the session directory
// sess.StatePath, sess.EventsPath, etc. are available
```

Auto-cleaned by `t.TempDir()`.

### 3.6 `internal/testutil.BDFake`

In-memory fake for `bd` CLI interactions (beads task manager):

```go
fake := testutil.NewBDFake(t,
    testutil.WithBeads([]bead.Bead{
        {ID: "1", Subject: "implement auth", Status: "open"},
    }),
)
// fake.ReadyCount() returns number of open beads
// fake.Claim("1") marks a bead as in_progress
```

Real `bd` only used in one opt-in smoke test (`AP_BD_SMOKE=1`).

## 4. Package-by-Package Test Expectations

### Existing Packages (already have code)

| Package | Unit Tests | Integration Tests |
|---------|-----------|-------------------|
| `internal/exec` | Command building, arg assembly | Process group signals, SIGTERM→SIGKILL cascade, bounded I/O caps |
| `internal/events` | Event serialization, field validation | Append-only flock writes, concurrent appenders, corrupt file recovery |
| `internal/state` | State transitions, history tracking | `ResumeFrom()` with partial state files, crash detection |
| `internal/context` | Context.json field assembly | Full context.json generation with real filesystem paths |
| `internal/resolve` | `${VAR}` substitution, edge cases | Template resolution with real stage files |
| `internal/result` | Output normalization, status parsing | — |
| `internal/termination` | Fixed iteration logic | — |
| `internal/validate` | Security check rules | Path traversal with real filesystem |
| `internal/stage` | Stage definition parsing | Multi-precedence resolution (project → plugin → builtin) |
| `internal/engine` | Provider registration | — |
| `pkg/provider` | Interface compliance | Fake CLI: stdin prompt, env propagation, timeout, stdout capture |

### New Packages (to be built)

| Package | Unit Tests | Integration Tests |
|---------|-----------|-------------------|
| `internal/spec` | Table-driven parse tests for each spec type (stage, chain, file, prompt), error messages, fuzz | Chain/YAML equivalence, round-trip parse→string→parse |
| `internal/runner` | Iteration logic with MockProvider, termination decisions, signal dispatch ordering | Full loop with FakeProviderBin, event ordering, resume from partial event log, escalate-pause behavior |
| `internal/signals` | Parse/validate signal payloads, handler chain ordering | Replay idempotency (`signal.dispatching` → terminal event), two-phase lifecycle, duplicate dispatch prevention |
| `internal/lock` | Lock/unlock logic, PID validation | Multi-process flock tests, stale PID cleanup, concurrent lock acquisition |
| `internal/session` | Launcher contract, option validation | `_run` request wiring, readiness handshake, parent→child tracking |
| `internal/judge` | Decision logic, consensus counting, threshold checks | Mock Haiku API response parsing, timeout/retry behavior |
| `internal/output` | Exit code mapping, error formatting, mode detection (TTY/JSON) | JSON vs human output golden files, pipe detection |
| `internal/fuzzy` | Levenshtein distance, synonym matching, flag recovery, fuzz | — |
| `internal/compile` | YAML parsing, node validation, input wiring, fuzz | Stage-to-stage data flow, parallel block compilation |
| `internal/parallel` | — | Cancellation semantics, first-finish behavior, cleanup of losing providers, race detector |
| `internal/messages` | Message parsing, format validation | Tailing partial lines, late subscriber catch-up, burst write handling |
| `internal/watch` | Event type filtering, pattern matching | Exec dispatch, config hook invocation |

## 5. CI Lanes

```bash
# Lane 1: Fast PR (target: < 30s)
go vet ./...
go test ./...

# Lane 2: Integration (target: < 2min)
go test -run Integration ./...

# Lane 3: Race detection (target: < 2min)
go test -race ./...

# Lane 4: E2E — optional, gated
AP_E2E=1 go test -run E2E ./...
AP_TMUX_E2E=1 go test -run Tmux ./...

# Lane 5: Fuzz — periodic/nightly
go test ./internal/spec -fuzz=FuzzParse -fuzztime=30s
go test ./internal/signals -fuzz=FuzzSignal -fuzztime=30s
go test ./internal/fuzzy -fuzz=FuzzMatch -fuzztime=30s
go test ./internal/compile -fuzz=FuzzCompile -fuzztime=30s
```

**Lane 1–3 are required for every PR.** Lane 4 runs on release branches or manual trigger. Lane 5 runs nightly and on-demand.

## 6. Golden File Policy

### Use golden files for:

- CLI help text (`ap --help`, `ap run --help`)
- Compiler output (pipeline compilation summaries)
- Error messages (structured error format)
- Any output with a stable, versioned format

### Do NOT use golden files for:

- JSON containing timestamps, durations, PIDs, or absolute paths
- Output that varies by platform or locale
- Anything where semantic assertions are clearer

### Conventions:

- Location: `testdata/` directory next to the test file
- Update mechanism: `go test -update` flag pattern (test checks `*update` flag, overwrites golden file if set)
- Review: Golden file changes show up in diffs, making format changes explicit

```go
var update = flag.Bool("update", false, "update golden files")

func TestHelpOutput(t *testing.T) {
    got := runHelp()
    golden := filepath.Join("testdata", "help.golden")
    if *update {
        os.WriteFile(golden, []byte(got), 0644)
        return
    }
    want, _ := os.ReadFile(golden)
    if diff := cmp.Diff(string(want), got); diff != "" {
        t.Errorf("help output mismatch (-want +got):\n%s", diff)
    }
}
```

## 7. Conventions

### File organization

- Test files: `foo_test.go` next to `foo.go`
- Test helpers: `internal/testutil/` (shared), or `testhelper_test.go` (package-private)
- Test data: `testdata/` directories (ignored by `go build`)

### Naming

| Kind | Prefix | Example |
|------|--------|---------|
| Unit test | `Test` | `TestSpecParse_StageWithCount` |
| Integration test | `TestIntegration_` | `TestIntegration_RunnerFullLoop` |
| E2E test | `TestE2E_` | `TestE2E_RunRalphSession` |
| Fuzz test | `Fuzz` | `FuzzSpecParse` |
| Benchmark | `Benchmark` | `BenchmarkSpecParse` |

### Dependencies

- **Standard library `testing` package** — no testify
- **`google/go-cmp`** — for readable diffs in assertions
- **No other test dependencies** unless they solve a concrete, demonstrable pain point

### Parallelism

- `t.Parallel()` on all unit tests
- Integration tests that touch shared filesystem: use `t.TempDir()`, then `t.Parallel()` is safe
- E2E tests: sequential by default (they share the real `ap` binary and may conflict)

### Filesystem

- `t.TempDir()` for all filesystem tests — auto-cleanup guaranteed
- Never write to the real project directory from tests
- Use `testutil.TempSession` for session directory scaffolding

### Test ergonomics

- Table-driven tests for any function with more than 2 interesting inputs
- Subtests (`t.Run`) for each table case — gives clear failure messages
- `t.Helper()` on all assertion helpers — points errors to the caller
- `t.Cleanup()` for resource teardown — runs even on `t.Fatal`

## 8. Crash/Replay Idempotency Tests

These are critical for `ap`'s event-sourced architecture and deserve special attention:

### What to test

1. **Replay produces identical state**: Given an `events.jsonl`, replaying from any prefix produces the correct state
2. **No duplicate side effects**: A crash after `signal.dispatching` but before `signal.dispatched` must not re-dispatch on resume
3. **Partial iteration recovery**: A crash mid-iteration resumes from the right point, not from scratch
4. **Event ordering invariants**: Events appear in causal order regardless of concurrent writes

### How to test

```go
func TestIntegration_ReplayIdempotency(t *testing.T) {
    sess := testutil.NewTempSession(t, "replay-test",
        testutil.WithEvents([]event.Event{
            {Type: "iteration.started", Iteration: 1},
            {Type: "signal.dispatching", Signal: "spawn", Iteration: 1},
            // crash here — no signal.dispatched
        }),
    )

    runner := runner.New(mock, runner.WithSession(sess))
    err := runner.Resume(context.Background())
    require.NoError(t, err)

    // Assert: spawn signal was NOT re-dispatched
    // Assert: iteration 1 resumed correctly
}
```

## 9. Relationship to Rev 10 Plan

This test strategy covers every package listed in the Rev 10 plan (both existing and new). The milestone delivery order determines which tests get written first:

| Milestone | Packages Under Test | Priority |
|-----------|-------------------|----------|
| Pre-M0 | `pkg/provider` (MockProvider), `internal/testutil` | **First** |
| M0a | `internal/spec` (stage+file), `internal/runner`, `internal/output` | High |
| M0b | `internal/spec` (chain), `internal/signals`, `internal/session` | High |
| M1 | `internal/lock`, `internal/runner` (resume) | High |
| M2 | `internal/judge`, `internal/signals` (full protocol) | Medium |
| M3 | `internal/compile`, `internal/parallel` | Medium |
| M4 | `internal/messages`, `internal/watch` | Medium |
| M5 | `internal/fuzzy`, E2E suite | Lower |

**Rule**: No milestone ships without its test column complete. Tests are part of the deliverable, not an afterthought.
