# Deep Performance Audit

Context: ${CTX}
Progress: ${PROGRESS}
Status: ${STATUS}

${CONTEXT}

## Your Task

Systematically identify and fix performance bottlenecks. Every optimization must be **proven correct** and **measured**.

### Methodology (MANDATORY)

**A) Baseline first.** Run the test suite and a representative workload. Record p50/p95/p99 latency, throughput, and peak memory with exact commands.

**B) Profile before proposing.** Capture CPU + allocation profiles. Identify the top 3-5 hotspots by % time before suggesting changes.

**C) Equivalence oracle.** Define explicit golden outputs + invariants that must not change.

**D) Isomorphism proof per change.** Every proposed diff must include a short proof sketch explaining why outputs cannot change.

**E) Opportunity matrix.** Rank candidates by (Impact x Confidence) / Effort before implementing.

**F) Minimal diffs.** One performance lever per change. No unrelated refactors.

**G) Regression guardrails.** Add benchmark thresholds or monitoring hooks.

### Optimization Patterns to Consider

- N+1 query/fetch elimination
- Zero-copy / buffer reuse / scatter-gather I/O
- Bounded queues + backpressure
- Memoization with cache invalidation
- Lazy evaluation / deferred computation
- Streaming/chunked processing for memory-bounded work
- Pre-computation and lookup tables
- Index-based lookup vs linear scan

### Process

1. **Read progress** for prior profiling results and optimizations.

2. **Profile the system** (or read existing profiles). Identify top hotspots.

3. **Pick ONE optimization target** — the highest (Impact x Confidence) / Effort.

4. **Implement the optimization:**
   - Write the proof sketch first
   - Make the minimal diff
   - Add a benchmark test

5. **Verify:** Run tests + benchmark. Compare before/after.

6. **Write status:**
   ```bash
   cat > ${STATUS} << 'EOF'
   {"decision": "continue", "summary": "Optimized <target>: <before> → <after> (<improvement>)"}
   EOF
   ```

## Update Progress

Append to ${PROGRESS}:

```markdown
## Performance Audit - Iteration ${ITERATION}
### Hotspot
- **Target**: [function/package]
- **Before**: [metric]
- **After**: [metric]
- **Proof sketch**: [why outputs unchanged]
- **Benchmark**: [command to reproduce]
---
```
