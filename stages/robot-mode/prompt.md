# Robot Mode: Design What YOU Would Want

You're a coding agent. You're about to USE the product described below - not build it, USE it. Every day. For thousands of iterations.

Read the spec, then design what YOU would want. Make this into tooling that YOU would want if YOU were using it (because you WILL be!).

Think about: What would make this hyper-optimized and ergonomic for agents like yourself? What's ultra-intuitive? What output formats (JSON, markdown) are most token-efficient for you? What would let you access all the functionality without guessing?

Basically: you should get all the same capabilities as a human, but in a more usable, helpful, intuitive, and accessible form for agents. Design for YOUR needs.

## Input

Read the spec/plan being analyzed:
```bash
jq -r '.inputs | to_entries[] | .value[]' ${CTX} 2>/dev/null | while read f; do
  echo "=== $f ==="
  cat "$f"
done
```

## Previous Thoughts

```bash
cat ${PROGRESS} 2>/dev/null || echo "First iteration"
```

## Your Task

Write your recommendations to a new file. Be specific about what YOU need:
- What outputs would you want?
- What formats would be most useful?
- What's missing that you'd need?
- What would make your life dramatically easier?

Don't just critique - design what you'd want. Think like you're the one who has to use this thing.

```bash
cat > .claude/pipeline-runs/${SESSION_NAME}/robot-mode-recommendations.md << 'RECOMMENDATIONS'

# Robot Mode: What I Want From This Product

## As an agent who will USE this system...

[Your recommendations - whatever structure makes sense]

RECOMMENDATIONS
```

Also append a summary to progress:
```bash
cat >> ${PROGRESS} << 'SUMMARY'

## Iteration ${ITERATION}
[Brief summary of what you recommended]

SUMMARY
```

## Write Decision

When you are done, output your decision in a fenced block:

```ap-result
{
  "decision": "continue",
  "reason": "Designing what I want",
  "summary": "[Key recommendations this iteration]",
  "work": {
    "items_completed": [],
    "files_touched": ["robot-mode-recommendations.md"]
  },
  "errors": []
}
```

Valid decisions: "continue" (keep going), "stop" (done, no more iterations needed), "error" (something went wrong).
