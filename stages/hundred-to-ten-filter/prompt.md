# The 100-to-10 Filter

Context: ${CTX}
Progress: ${PROGRESS}
Status: ${STATUS}

${CONTEXT}

## Your Task

Generate your top 10 most brilliant ideas for making this system far more compelling, useful, intuitive, versatile, powerful, robust, and reliable.

**But don't just think of 10 ideas.** Think HARD and generate **ONE HUNDRED ideas** first. Then ruthlessly filter to your **10 VERY BEST** — the most brilliant, clever, radically innovative, and powerful ideas.

### Process

1. **Read the project** — understand what it does, how it works, what it's for. Read progress for prior context.

2. **Generate 100 ideas** (brief one-liners). Push past the obvious. Go broad:
   - Developer experience improvements
   - Performance and reliability
   - New capabilities
   - Simplifications and cuts
   - Integration points
   - Workflow improvements
   - Agent-friendliness
   - Error recovery
   - Observability

3. **Filter ruthlessly.** For each of the 100, ask:
   - Is this actually brilliant, or just "nice to have"?
   - Is it pragmatic to implement, or would it introduce crushing complexity?
   - Does it make the system 10x better at something, or just 10% better?

4. **Present your 10 best.** For each:
   - What it is (concrete, specific, actionable)
   - Why it's brilliant (not just good)
   - How to implement it (sketch, not full spec)
   - Confidence it improves the project (0-100%)

5. **Write status**:
   ```bash
   cat > ${STATUS} << 'EOF'
   {"decision": "stop", "summary": "Generated 100 ideas, filtered to top 10"}
   EOF
   ```

## Write Output

Write the full 100 ideas + top 10 analysis to ${PROGRESS}.
