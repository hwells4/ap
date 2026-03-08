package extract

import (
	"encoding/json"
	"strings"
)

// Result holds the extracted decision from provider stdout.
type Result struct {
	Decision string  `json:"decision"`
	Summary  string  `json:"summary"`
	Reason   string  `json:"reason,omitempty"`
	Signals  Signals `json:"signals,omitempty"`
}

// Signals holds optional agent signals from the ap-result block.
type Signals struct {
	Inject   string          `json:"inject,omitempty"`
	Escalate *EscalateSignal `json:"escalate,omitempty"`
	Spawn    json.RawMessage `json:"spawn,omitempty"`
	Message  *MessageSignal  `json:"message,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

// MessageSignal represents a message from one provider to another in a swarm block.
type MessageSignal struct {
	To      string `json:"to"`
	Content string `json:"content"`
}

// EscalateSignal represents an escalation request from the agent.
type EscalateSignal struct {
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Options []string `json:"options,omitempty"`
}

// Source identifies how the decision was determined.
type Source string

const (
	// SourceFencedBlock means decision came from a ```ap-result block.
	SourceFencedBlock Source = "fenced_block"
	// SourceExitCode means decision was inferred from non-zero exit code.
	SourceExitCode Source = "exit_code"
	// SourceDefault means no signal was found; defaulting to continue.
	SourceDefault Source = "default"
)

const maxTailLen = 200

// Extract parses provider stdout for decision information.
//
// Strategy chain (first success wins):
//  1. Parse last ```ap-result fenced block from stdout -> parse JSON -> validate
//  2. Exit code != 0 -> {decision: "error", summary: last 200 chars of stderr}
//  3. Default -> {decision: "continue", summary: last 200 chars of stdout}
func Extract(stdout string, exitCode int) (Result, Source, error) {
	// Strategy 1: Look for the LAST ```ap-result fenced block
	if result, ok := extractFencedBlock(stdout); ok {
		return result, SourceFencedBlock, nil
	}

	// Strategy 2: Non-zero exit code -> error
	if exitCode != 0 {
		return Result{
			Decision: "error",
			Summary:  tail(stdout, maxTailLen),
		}, SourceExitCode, nil
	}

	// Strategy 3: Default -> continue
	return Result{
		Decision: "continue",
		Summary:  tail(stdout, maxTailLen),
	}, SourceDefault, nil
}

// extractFencedBlock finds the LAST ```ap-result ... ``` block in stdout
// and attempts to parse its JSON content. Supports both multi-line fenced
// blocks and inline single-line blocks (e.g. ```ap-result {...} ```).
func extractFencedBlock(stdout string) (Result, bool) {
	lines := strings.Split(stdout, "\n")

	lastBlockStart := -1
	lastBlockEnd := -1

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		openTicks := apResultFenceTicks(trimmed)
		if openTicks == 0 {
			// Check for inline ap-result block within this line.
			if result, ok := extractInlineFencedBlock(trimmed); ok {
				return result, true
			}
			continue
		}
		// Found opening fence, look for closing fence with >= openTicks backticks
		foundClose := false
		for j := i + 1; j < len(lines); j++ {
			closeTrimmed := strings.TrimSpace(lines[j])
			if isClosingFence(closeTrimmed, openTicks) {
				lastBlockStart = i + 1
				lastBlockEnd = j
				i = j // skip past this block
				foundClose = true
				break
			}
		}
		// If no closing fence on subsequent lines, try inline extraction
		// (the entire block may be on this single line).
		if !foundClose {
			if result, ok := extractInlineFencedBlock(trimmed); ok {
				return result, true
			}
		}
	}

	if lastBlockStart < 0 || lastBlockEnd < 0 {
		return Result{}, false
	}

	// Extract content between fences
	blockLines := lines[lastBlockStart:lastBlockEnd]
	content := strings.Join(blockLines, "\n")
	content = strings.TrimSpace(content)

	if content == "" {
		return Result{}, false
	}

	var result Result
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return Result{}, false // malformed JSON -> fall through
	}

	// Validate decision
	result.Decision = strings.ToLower(strings.TrimSpace(result.Decision))
	if result.Decision == "" {
		result.Decision = "continue"
	}

	return result, true
}

// extractInlineFencedBlock handles the case where an agent emits the entire
// ap-result block on a single line, e.g.:
//
//	some text ```ap-result {"decision":"stop","summary":"Done"} ```
//
// It scans for ```ap-result (case-insensitive) anywhere in the line, then
// finds the closing ``` and parses the JSON between them.
func extractInlineFencedBlock(line string) (Result, bool) {
	lower := strings.ToLower(line)
	// Find the opening fence marker anywhere in the line.
	idx := strings.Index(lower, "```ap-result")
	if idx < 0 {
		return Result{}, false
	}
	// Count backticks at the opening fence.
	openStart := idx
	ticks := 0
	for openStart+ticks < len(line) && line[openStart+ticks] == '`' {
		ticks++
	}
	if ticks < 3 {
		return Result{}, false
	}
	// Content starts after the opening fence marker ("```ap-result" portion).
	contentStart := openStart + ticks
	// Skip "ap-result" (case-insensitive) after the backticks.
	rest := line[contentStart:]
	lowerRest := strings.ToLower(rest)
	if !strings.HasPrefix(lowerRest, "ap-result") {
		return Result{}, false
	}
	contentStart += len("ap-result")
	rest = line[contentStart:]

	// Find the closing fence: at least `ticks` backticks.
	closeIdx := findClosingFenceInline(rest, ticks)
	if closeIdx < 0 {
		return Result{}, false
	}

	content := strings.TrimSpace(rest[:closeIdx])
	if content == "" {
		return Result{}, false
	}

	var result Result
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return Result{}, false
	}

	result.Decision = strings.ToLower(strings.TrimSpace(result.Decision))
	if result.Decision == "" {
		result.Decision = "continue"
	}
	return result, true
}

// findClosingFenceInline finds a closing fence (>= minTicks consecutive
// backticks) within a string. Returns the index of the first backtick of
// the closing fence, or -1 if not found.
func findClosingFenceInline(s string, minTicks int) int {
	i := 0
	for i < len(s) {
		if s[i] != '`' {
			i++
			continue
		}
		start := i
		for i < len(s) && s[i] == '`' {
			i++
		}
		if i-start >= minTicks {
			return start
		}
	}
	return -1
}

// apResultFenceTicks returns the number of backticks in the opening fence
// if the line is a valid ap-result fence opener, or 0 otherwise.
func apResultFenceTicks(line string) int {
	lower := strings.ToLower(line)
	// Count leading backticks
	ticks := 0
	for _, ch := range lower {
		if ch == '`' {
			ticks++
		} else {
			break
		}
	}
	if ticks < 3 {
		return 0
	}
	rest := lower[ticks:]
	if strings.HasPrefix(rest, "ap-result") {
		return ticks
	}
	return 0
}

// isClosingFence checks if the line is a closing fence with at least minTicks backticks.
func isClosingFence(line string, minTicks int) bool {
	// A closing fence is a line that is only backticks (after trimming)
	if len(line) == 0 {
		return false
	}
	for _, ch := range line {
		if ch != '`' {
			return false
		}
	}
	return len(line) >= minTicks
}

func tail(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = sanitizeFenceContent(s)
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[len(runes)-maxLen:])
}

// sanitizeFenceContent strips ```ap-result fenced blocks from fallback
// summary text so that raw fence markers don't leak into commit messages
// or other user-facing output.
func sanitizeFenceContent(s string) string {
	lower := strings.ToLower(s)
	idx := strings.Index(lower, "```ap-result")
	if idx < 0 {
		return s
	}
	// Try to extract the summary from the JSON inside the fence.
	// If successful, use that instead of the raw text.
	if result, ok := extractInlineFencedBlock(s[idx:]); ok && result.Summary != "" {
		prefix := strings.TrimSpace(s[:idx])
		if prefix != "" {
			return prefix + " " + result.Summary
		}
		return result.Summary
	}
	// If we can't parse it, strip the fence markers and keep what's before.
	prefix := strings.TrimSpace(s[:idx])
	if prefix != "" {
		return prefix
	}
	return s
}
