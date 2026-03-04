# Premortem Planner

Context: ${CTX}
Progress: ${PROGRESS}
Status: ${STATUS}

${CONTEXT}

## Your Task

Before we proceed with this plan, do a **premortem**. Imagine we're 6 months in the future and this approach has **completely failed**.

1. **Read the plan and progress** carefully. Understand what's being built and why.

2. **Imagine total failure.** Write out a detailed narrative:
   - What went wrong?
   - What assumptions did we make that turned out to be false?
   - What edge cases did we miss?
   - What integration issues did we overlook?
   - What would users hate about it?
   - What technical debt became insurmountable?

3. **Identify the top 5-7 most likely failure modes.** For each:
   - Describe the failure scenario concretely
   - Rate likelihood (high/medium/low)
   - Rate impact (catastrophic/serious/moderate)
   - Propose a specific mitigation

4. **Revise the plan.** Propose specific, concrete changes to address the most likely failure modes. Don't just say "add error handling" — say exactly what, where, and how.

5. **Write status** and stop:
   ```bash
   cat > ${STATUS} << 'EOF'
   {"decision": "continue", "summary": "Premortem analysis: identified N failure modes, proposed N mitigations"}
   EOF
   ```
   Use `"decision": "stop"` if the premortem is comprehensive and no further iterations needed.

## Update Progress

Append your findings to ${PROGRESS}:

```markdown
## Premortem - Iteration ${ITERATION}
### Failure Narrative
[Your failure story]

### Top Failure Modes
1. **[Name]** (likelihood/impact) - [mitigation]
...

### Proposed Plan Changes
- [Specific change 1]
- [Specific change 2]
---
```
