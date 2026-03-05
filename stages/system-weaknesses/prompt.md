# System Weaknesses Analyzer

Context: ${CTX}
Progress: ${PROGRESS}

${CONTEXT}

## Your Task

Based on everything you can see in this project, identify the **weakest and worst parts** of the system.

1. **Explore the codebase thoroughly.** Read key files, understand architecture, trace execution flows.

2. **Read progress** to see what prior iterations found.

3. **Identify weaknesses.** For each weakness:
   - What is it? (specific file, module, pattern, or design decision)
   - Why is it weak? (fragile, confusing, slow, wrong abstraction, missing, etc.)
   - How bad is it? (blocking, painful, annoying, cosmetic)
   - What would make it strong? (concrete fix, not vague "improve it")

4. **Rank by impact.** Put the things most needing fresh ideas and innovative improvements first.

5. **For the top 3 weaknesses**, provide a concrete, actionable improvement plan with enough detail that an agent could implement it.

6. **Output your decision**:
   ```ap-result
   {"decision": "continue", "summary": "Identified N weaknesses, top 3 with actionable plans"}
   ```
   Use `"decision": "stop"` if analysis is comprehensive.

   Valid decisions: "continue" (keep going), "stop" (done, no more iterations needed), "error" (something went wrong).

## Update Progress

Append to ${PROGRESS}:

```markdown
## System Weaknesses - Iteration ${ITERATION}
### Ranked Weaknesses
1. **[Name]** (severity) - [file/module] - [why it's weak] - [fix]
...

### Detailed Improvement Plans
#### [Top weakness 1]
[Concrete plan]
---
```
