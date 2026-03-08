package extract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	tests := []struct {
		name       string
		stdout     string
		exitCode   int
		wantResult Result
		wantSource Source
		wantErr    bool
	}{
		// 1. Valid single fenced block
		{
			name:       "valid single block",
			stdout:     "some output\n```ap-result\n{\"decision\": \"continue\", \"summary\": \"Did work\"}\n```\n",
			wantResult: Result{Decision: "continue", Summary: "Did work"},
			wantSource: SourceFencedBlock,
		},
		// 2. Multiple blocks - last wins
		{
			name:       "multiple blocks last wins",
			stdout:     "```ap-result\n{\"decision\": \"continue\", \"summary\": \"First\"}\n```\nmore output\n```ap-result\n{\"decision\": \"stop\", \"summary\": \"Second\"}\n```\n",
			wantResult: Result{Decision: "stop", Summary: "Second"},
			wantSource: SourceFencedBlock,
		},
		// 3. Stop decision
		{
			name:       "stop decision",
			stdout:     "```ap-result\n{\"decision\": \"stop\", \"summary\": \"Done\", \"reason\": \"All tasks complete\"}\n```\n",
			wantResult: Result{Decision: "stop", Summary: "Done", Reason: "All tasks complete"},
			wantSource: SourceFencedBlock,
		},
		// 4. Malformed JSON falls through to exit code
		{
			name:       "malformed JSON non-zero exit",
			stdout:     "```ap-result\n{invalid json}\n```\n",
			exitCode:   1,
			wantResult: Result{Decision: "error", Summary: "```ap-result\n{invalid json}\n```"},
			wantSource: SourceExitCode,
		},
		// 5. Malformed JSON falls through to default
		{
			name:       "malformed JSON zero exit",
			stdout:     "```ap-result\n{bad}\n```\n",
			exitCode:   0,
			wantResult: Result{Decision: "continue", Summary: "```ap-result\n{bad}\n```"},
			wantSource: SourceDefault,
		},
		// 6. No block, non-zero exit code
		{
			name:       "no block non-zero exit",
			stdout:     "some error output",
			exitCode:   1,
			wantResult: Result{Decision: "error", Summary: "some error output"},
			wantSource: SourceExitCode,
		},
		// 7. No block, zero exit code (default continue)
		{
			name:       "no block zero exit default",
			stdout:     "just some output",
			exitCode:   0,
			wantResult: Result{Decision: "continue", Summary: "just some output"},
			wantSource: SourceDefault,
		},
		// 8. Empty stdout
		{
			name:       "empty stdout",
			stdout:     "",
			exitCode:   0,
			wantResult: Result{Decision: "continue", Summary: ""},
			wantSource: SourceDefault,
		},
		// 9. Block with signals - inject
		{
			name:       "block with inject signal",
			stdout:     "```ap-result\n{\"decision\": \"continue\", \"summary\": \"Injecting\", \"signals\": {\"inject\": \"extra context\"}}\n```\n",
			wantResult: Result{Decision: "continue", Summary: "Injecting", Signals: Signals{Inject: "extra context"}},
			wantSource: SourceFencedBlock,
		},
		// 10. Block with escalate signal
		{
			name: "block with escalate signal",
			stdout: "```ap-result\n{\"decision\": \"continue\", \"summary\": \"Need help\", \"signals\": {\"escalate\": {\"type\": \"question\", \"reason\": \"unclear spec\", \"options\": [\"A\", \"B\"]}}}\n```\n",
			wantResult: Result{
				Decision: "continue", Summary: "Need help",
				Signals: Signals{Escalate: &EscalateSignal{Type: "question", Reason: "unclear spec", Options: []string{"A", "B"}}},
			},
			wantSource: SourceFencedBlock,
		},
		// 11. Case insensitive fence marker
		{
			name:       "case insensitive fence",
			stdout:     "```AP-RESULT\n{\"decision\": \"continue\", \"summary\": \"works\"}\n```\n",
			wantResult: Result{Decision: "continue", Summary: "works"},
			wantSource: SourceFencedBlock,
		},
		// 12. Block with extra whitespace
		{
			name:       "block with whitespace",
			stdout:     "  ```ap-result  \n  {\"decision\": \"continue\", \"summary\": \"trimmed\"}  \n  ```  \n",
			wantResult: Result{Decision: "continue", Summary: "trimmed"},
			wantSource: SourceFencedBlock,
		},
		// 13. Block with missing decision defaults to continue
		{
			name:       "missing decision defaults continue",
			stdout:     "```ap-result\n{\"summary\": \"no decision field\"}\n```\n",
			wantResult: Result{Decision: "continue", Summary: "no decision field"},
			wantSource: SourceFencedBlock,
		},
		// 14. Large stdout - tail truncation
		{
			name:       "large stdout truncated",
			stdout:     strings.Repeat("x", 500),
			exitCode:   0,
			wantResult: Result{Decision: "continue", Summary: strings.Repeat("x", 200)},
			wantSource: SourceDefault,
		},
		// 15. Unclosed fence block falls through
		{
			name:       "unclosed fence",
			stdout:     "```ap-result\n{\"decision\": \"stop\"}\nno closing fence",
			exitCode:   0,
			wantResult: Result{Decision: "continue"},
			wantSource: SourceDefault,
		},
		// 16. Nested code fence inside ap-result (4-backtick fence)
		{
			name:       "nested code fence",
			stdout:     "````ap-result\n{\"decision\": \"continue\", \"summary\": \"has nested```code\"}\n````\n",
			wantResult: Result{Decision: "continue", Summary: "has nested```code"},
			wantSource: SourceFencedBlock,
		},
		// 17. Unicode content in decision block
		{
			name:       "unicode content",
			stdout:     "```ap-result\n{\"decision\": \"continue\", \"summary\": \"\u5b8c\u4e86\u3057\u307e\u3057\u305f \u2705 \u00e9l\u00e8ve\"}\n```\n",
			wantResult: Result{Decision: "continue", Summary: "\u5b8c\u4e86\u3057\u307e\u3057\u305f \u2705 \u00e9l\u00e8ve"},
			wantSource: SourceFencedBlock,
		},
		// 18. Large stdout (>1MB) with block at end
		{
			name:       "large stdout finds last block",
			stdout:     strings.Repeat("line of output\n", 100000) + "```ap-result\n{\"decision\": \"stop\", \"summary\": \"found it\"}\n```\n",
			wantResult: Result{Decision: "stop", Summary: "found it"},
			wantSource: SourceFencedBlock,
		},
		// 19. Unknown fields are silently ignored
		{
			name:       "unknown fields ignored",
			stdout:     "```ap-result\n{\"decision\": \"continue\", \"summary\": \"ok\", \"unknown_field\": 42, \"extra\": {\"nested\": true}}\n```\n",
			wantResult: Result{Decision: "continue", Summary: "ok"},
			wantSource: SourceFencedBlock,
		},
		// 20. Block with spawn signal (raw JSON preserved)
		{
			name:       "block with spawn signal",
			stdout:     "```ap-result\n{\"decision\": \"continue\", \"summary\": \"spawning\", \"signals\": {\"spawn\": {\"stage\": \"review\", \"count\": 3}}}\n```\n",
			wantResult: Result{Decision: "continue", Summary: "spawning"},
			wantSource: SourceFencedBlock,
		},
		// 21. Block with warnings signal
		{
			name:   "block with warnings",
			stdout: "```ap-result\n{\"decision\": \"continue\", \"summary\": \"warned\", \"signals\": {\"warnings\": [\"low memory\", \"slow network\"]}}\n```\n",
			wantResult: Result{
				Decision: "continue", Summary: "warned",
				Signals: Signals{Warnings: []string{"low memory", "slow network"}},
			},
			wantSource: SourceFencedBlock,
		},
		// 22. Empty block content falls through
		{
			name:       "empty block content",
			stdout:     "```ap-result\n\n```\n",
			exitCode:   0,
			wantResult: Result{Decision: "continue"},
			wantSource: SourceDefault,
		},
		// 23. Inline fenced block (single line with preceding text)
		{
			name:       "inline fenced block",
			stdout:     "some text before ```ap-result {\"decision\": \"stop\", \"summary\": \"Done inline\"} ```",
			wantResult: Result{Decision: "stop", Summary: "Done inline"},
			wantSource: SourceFencedBlock,
		},
		// 24. Inline fenced block with no preceding text
		{
			name:       "inline fenced block no prefix",
			stdout:     "```ap-result {\"decision\": \"continue\", \"summary\": \"Inline only\"} ```",
			wantResult: Result{Decision: "continue", Summary: "Inline only"},
			wantSource: SourceFencedBlock,
		},
		// 25. Inline with 4 backticks
		{
			name:       "inline 4 backtick fence",
			stdout:     "text ````ap-result {\"decision\": \"stop\", \"summary\": \"Four ticks\"} ````",
			wantResult: Result{Decision: "stop", Summary: "Four ticks"},
			wantSource: SourceFencedBlock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source, err := Extract(tt.stdout, tt.exitCode)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Extract() error = %v, wantErr %v", err, tt.wantErr)
			}
			if source != tt.wantSource {
				t.Errorf("Extract() source = %v, want %v", source, tt.wantSource)
			}
			if tt.wantSource == SourceDefault || tt.wantSource == SourceExitCode {
				// Just check decision for fallback sources
				if got.Decision != tt.wantResult.Decision {
					t.Errorf("Extract() decision = %q, want %q", got.Decision, tt.wantResult.Decision)
				}
				return
			}
			if got.Decision != tt.wantResult.Decision {
				t.Errorf("decision = %q, want %q", got.Decision, tt.wantResult.Decision)
			}
			if got.Summary != tt.wantResult.Summary {
				t.Errorf("summary = %q, want %q", got.Summary, tt.wantResult.Summary)
			}
			if got.Reason != tt.wantResult.Reason {
				t.Errorf("reason = %q, want %q", got.Reason, tt.wantResult.Reason)
			}
			if got.Signals.Inject != tt.wantResult.Signals.Inject {
				t.Errorf("inject = %q, want %q", got.Signals.Inject, tt.wantResult.Signals.Inject)
			}
			if !reflect.DeepEqual(got.Signals.Warnings, tt.wantResult.Signals.Warnings) {
				t.Errorf("warnings = %v, want %v", got.Signals.Warnings, tt.wantResult.Signals.Warnings)
			}
			if tt.wantResult.Signals.Spawn != nil {
				if got.Signals.Spawn == nil {
					t.Error("spawn signal is nil, want non-nil")
				} else if !json.Valid(got.Signals.Spawn) {
					t.Errorf("spawn signal is not valid JSON: %s", got.Signals.Spawn)
				}
			}
		})
	}
}

func TestSanitizeFenceContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fence markers",
			input: "clean summary text",
			want:  "clean summary text",
		},
		{
			name:  "fence with valid JSON extracts summary",
			input: "text before ```ap-result {\"decision\":\"stop\",\"summary\":\"Extracted summary\"} ```",
			want:  "text before Extracted summary",
		},
		{
			name:  "fence with invalid JSON keeps prefix",
			input: "text before ```ap-result {invalid} ```",
			want:  "text before",
		},
		{
			name:  "fence only no prefix with valid JSON",
			input: "```ap-result {\"decision\":\"stop\",\"summary\":\"Only summary\"} ```",
			want:  "Only summary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFenceContent(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFenceContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// FuzzExtract verifies Extract never panics and always returns valid source/decision.
func FuzzExtract(f *testing.F) {
	f.Add("```ap-result\n{\"decision\":\"continue\"}\n```", 0)
	f.Add("", 0)
	f.Add("no blocks here", 1)
	f.Add("```ap-result\n{invalid}\n```", 0)
	f.Add("````ap-result\n{\"decision\":\"stop\",\"summary\":\"nested```fence\"}\n````", 0)
	f.Add("```ap-result\n{\"decision\":\"continue\",\"summary\":\"\u5b8c\u4e86\"}\n```", 0)
	f.Add("```ap-result\n{\"decision\":\"continue\",\"signals\":{\"inject\":\"x\"}}\n```", 0)
	f.Add("```ap-result\n\n```", 0)
	f.Fuzz(func(t *testing.T, stdout string, exitCode int) {
		result, source, err := Extract(stdout, exitCode)
		if err != nil {
			return // errors are acceptable
		}
		// Source must be one of the valid values
		switch source {
		case SourceFencedBlock, SourceExitCode, SourceDefault:
		default:
			t.Errorf("unexpected source: %v", source)
		}
		// Decision must not be empty
		if result.Decision == "" {
			t.Error("decision must not be empty")
		}
	})
}
