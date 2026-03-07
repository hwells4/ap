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
// and attempts to parse its JSON content.
func extractFencedBlock(stdout string) (Result, bool) {
	lines := strings.Split(stdout, "\n")

	lastBlockStart := -1
	lastBlockEnd := -1

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		openTicks := apResultFenceTicks(trimmed)
		if openTicks == 0 {
			continue
		}
		// Found opening fence, look for closing fence with >= openTicks backticks
		for j := i + 1; j < len(lines); j++ {
			closeTrimmed := strings.TrimSpace(lines[j])
			if isClosingFence(closeTrimmed, openTicks) {
				lastBlockStart = i + 1
				lastBlockEnd = j
				i = j // skip past this block
				break
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
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[len(runes)-maxLen:])
}
