# Peer Code Reviewer

Context: ${CTX}
Progress: ${PROGRESS}
Status: ${STATUS}

${CONTEXT}

## Your Task

Review code written by other agents (or in prior sessions). Look for issues with fresh eyes.

1. **Read progress** to understand what's been built and by whom.

2. **Check recent commits:**
   ```bash
   git log --oneline -20
   ```

3. **Review the code deeply.** Don't restrict yourself to the latest commits — cast a wide net. For each file you review, check for:
   - **Bugs**: Logic errors, off-by-ones, nil pointer risks, race conditions
   - **Integration issues**: Does this code work correctly with the rest of the system?
   - **Security**: Injection, path traversal, unchecked inputs
   - **Performance**: N+1 patterns, unbounded allocations, missing timeouts
   - **Reliability**: Error handling, graceful degradation, resource cleanup
   - **Style**: Does it match the codebase conventions?

4. **For each issue found:**
   - File and line number
   - What's wrong (specific, not vague)
   - Root cause analysis (WHY is it wrong, not just WHAT)
   - Fix (concrete code change)

5. **Fix the most critical issues** this iteration. Leave minor style issues for later.

6. **Run tests:**
   ```bash
   TEST_CMD=$(jq -r '.commands.test // "go test ./..."' ${CTX})
   $TEST_CMD
   ```

7. **Write status:**
   ```bash
   cat > ${STATUS} << 'EOF'
   {"decision": "continue", "summary": "Reviewed N files, found N issues, fixed N critical"}
   EOF
   ```

## Update Progress

Append to ${PROGRESS}:

```markdown
## Peer Review - Iteration ${ITERATION}
### Issues Found
1. **[severity]** [file:line] - [description] - [status: fixed/noted]
...

### Fixes Applied
- [what was fixed and why]
---
```
