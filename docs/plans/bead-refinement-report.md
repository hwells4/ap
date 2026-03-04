# Bead Refinement Report

## Scope Reviewed
- Plan: `docs/plans/2026-02-27-refined-go-rewrite-plan.md` (Rev 10)
- Epic: `agent-pipelines-fgl`
- Initial state: 49 open Rev 10 task beads
- Major issue found: 45/49 beads had placeholder descriptions (`- type: task`) and empty acceptance criteria

## 1) Beads Updated (what and why)
- Updated all original 49 task beads with implementation-ready descriptions, testable acceptance criteria, and 30-120 minute estimates.
- Reworked ambiguous titles to verb-first, scoped actions (for example splitting command bundles and clarifying milestone intent).
- Set all open task beads under the epic as explicit children (`parent: agent-pipelines-fgl`) for consistent hierarchy.

### Updated Original Bead Sets
- Pre-M0: `agent-pipelines-fgl.1`, `agent-pipelines-bn0`, `agent-pipelines-7xt`, `agent-pipelines-tq2`, `agent-pipelines-o2c`, `agent-pipelines-9da`
- M0a: `agent-pipelines-vs5`, `agent-pipelines-9mj`, `agent-pipelines-3lu`, `agent-pipelines-4vu`, `agent-pipelines-myu`, `agent-pipelines-w1u`, `agent-pipelines-ua8`
- M0b: `agent-pipelines-cde`, `agent-pipelines-345`, `agent-pipelines-sqf`, `agent-pipelines-dmt`, `agent-pipelines-2uo`, `agent-pipelines-a17`, `agent-pipelines-86k`, `agent-pipelines-pir`, `agent-pipelines-uf1`, `agent-pipelines-1ws`, `agent-pipelines-cvh`, `agent-pipelines-35p`
- M0c: `agent-pipelines-4sr`, `agent-pipelines-3dn`, `agent-pipelines-mkq`, `agent-pipelines-21i`, `agent-pipelines-rjm`
- M1: `agent-pipelines-jyt`, `agent-pipelines-isx`, `agent-pipelines-mqb`
- M2: `agent-pipelines-pub`, `agent-pipelines-lv1`, `agent-pipelines-jlq`, `agent-pipelines-x1o`, `agent-pipelines-a16`
- M3: `agent-pipelines-jhp`, `agent-pipelines-2xk`, `agent-pipelines-8y5`, `agent-pipelines-45p`
- M4: `agent-pipelines-7y3`, `agent-pipelines-zjq`, `agent-pipelines-h3z`, `agent-pipelines-8k3`
- M5: `agent-pipelines-219`, `agent-pipelines-dmg`, `agent-pipelines-awu`

## 2) Beads Added (what gap they fill)
Added 14 new open beads to close plan coverage gaps and right-size scope:

- `agent-pipelines-fgl.2` `[M0b] Implement ap logs command`
  - Gap filled: split oversized command bead; explicit logs JSON/follow behavior
- `agent-pipelines-fgl.3` `[M0b] Implement ap clean command`
  - Gap filled: split oversized command bead; idempotent cleanup and safety rules
- `agent-pipelines-fgl.4` `[M0b] Write AGENTS.md CLI contract`
  - Gap filled: explicit AGENTS.md deliverable from plan
- `agent-pipelines-fgl.5` `[M0b] Emit lifecycle telemetry events`
  - Gap filled: Contract 5 required event emission (`session.*`, `iteration.*`)
- `agent-pipelines-fgl.6` `[M0b] Release in-progress beads on session crash or kill`
  - Gap filled: prompt contract/decision parity for bead cleanup
- `agent-pipelines-fgl.7` `[M2] Implement ~/.config/ap/config.yaml loader`
  - Gap filled: config parsing for handlers/limits/hooks/callback host
- `agent-pipelines-fgl.8` `[M2] Implement escalation callback listener and token auth`
  - Gap filled: callback flow from signal contract (`callback_url`, `callback_token`)
- `agent-pipelines-fgl.10` `[M0a] Embed built-in stage prompts via go:embed`
  - Gap filled: prompt discovery/embedding decision in plan
- `agent-pipelines-fgl.12` `[M2] Implement --on-escalate CLI flag`
  - Gap filled: explicit one-off handler override path
- `agent-pipelines-fgl.14` `[M1] Write M1 tests`
- `agent-pipelines-fgl.15` `[M2] Write M2 tests`
- `agent-pipelines-fgl.16` `[M3] Write M3 tests`
- `agent-pipelines-fgl.17` `[M4] Write M4 tests`
  - Gap filled: missing milestone-specific test beads beyond M0a/M0b/M5
- `agent-pipelines-fgl.18` `[M0a] Enforce template variable resolution contract`
  - Gap filled: explicit Contract 2 parity (single pass, undefined variables stay literal, newline preservation)

### Duplicate beads closed during cleanup
- `agent-pipelines-fgl.9` (duplicate AGENTS.md scope)
- `agent-pipelines-fgl.11` (duplicate config parsing scope)
- `agent-pipelines-fgl.13` (duplicate bead crash-cleanup scope)

## 3) Dependencies Fixed
- Removed stale/overly coarse dependency links inherited from initial set (43 non-epic links).
- Rebuilt milestone-aware dependency graph with explicit integration points (139 non-epic links).
- Key rewires made:
  - M0b no longer hangs uniformly on `M0a tests`; command and launcher beads now depend on actual prerequisites (`internal/spec`, `_run`, launchers, locking, lifecycle events).
  - M1 now correctly chains `judge invocation -> judgment strategy -> runner wiring`.
  - M2 now explicitly depends on config loading, signal-chain wiring, and callback listener work.
  - M3 now depends on spec/compiler work (not Codex provider).
  - M4 and M5 now depend on pipeline/parallel prerequisites and watch/log/config hooks.
  - Split command beads (`kill`, `logs`, `clean`) each have clear dependency and test wiring.
- Validation: `bd dep cycles` reports no cycles.

## 4) Remaining Concerns
- Bead count increased from 49 to 63 open tasks; this is intentional for coverage and sizing but milestone tracking should be re-baselined.
- M2 has many integration edges (handlers, config, callback, retry, provider); if throughput stalls, consider temporary assignment by subdomain (provider vs signaling).
- State snapshot boundedness details (`recent_iterations`, `files_touched`, `signals` caps and eviction) are included indirectly; if desired, add one explicit acceptance clause to `agent-pipelines-pir` for bound enforcement tests.
- `max_concurrent_providers` queueing behavior is referenced via config and parallel beads; if treated as strict v1 requirement, consider adding one dedicated enforcement bead.

## Final State Summary
- Open Rev 10 task beads: 63
- All open beads now have non-placeholder descriptions and non-empty acceptance criteria
- All open beads have estimates in the 30-120 minute range
- Dependency graph is acyclic and milestone-ordered
