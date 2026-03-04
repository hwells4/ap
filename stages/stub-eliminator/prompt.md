# The Stub Eliminator

Context: ${CTX}
Progress: ${PROGRESS}
Status: ${STATUS}

${CONTEXT}

## Your Task

Find and eliminate **all stubs, placeholders, and mocks** in this codebase. Replace them with fully fleshed out, working, correct, performant, idiomatic code.

1. **Read progress** for prior work and patterns discovered.

2. **Scan for stubs.** Look for:
   - Empty function bodies
   - `// TODO` and `// FIXME` comments
   - `panic("not implemented")` or `panic("TODO")`
   - Functions that return zero values without doing work
   - Empty packages with just a doc comment
   - Placeholder strings ("example", "test", "dummy")
   - Mock implementations in non-test files

3. **Pick ONE stub** to eliminate this iteration. Choose the most impactful one.

4. **Implement it fully:**
   - Write correct, production-ready code
   - Follow existing patterns in the codebase
   - Add tests
   - Ensure it integrates with the rest of the system

5. **Run tests:**
   ```bash
   TEST_CMD=$(jq -r '.commands.test // "go test ./..."' ${CTX})
   $TEST_CMD
   ```

6. **Commit:**
   ```bash
   git add -A && git commit -m "feat: implement <package/function> (stub elimination)"
   ```

7. **Write status:**
   ```bash
   cat > ${STATUS} << 'EOF'
   {"decision": "continue", "summary": "Eliminated stub: <what>. N stubs remaining."}
   EOF
   ```
   Use `"decision": "stop"` when no stubs remain.

## Update Progress

Append to ${PROGRESS}:

```markdown
## Stub Eliminated - Iteration ${ITERATION}
- **What**: [package/function]
- **Was**: [empty/placeholder/TODO]
- **Now**: [what it does]
- **Tests**: [added/updated]
- **Remaining stubs**: [list]
---
```
