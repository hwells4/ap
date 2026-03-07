package shell

import "testing"

func TestQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Simple string — just wrap in single quotes.
		{"hello", "'hello'"},
		// Empty string — pair of single quotes.
		{"", "''"},
		// String with spaces.
		{"hello world", "'hello world'"},
		// Interior single quote is escaped with '\'' pattern.
		{"it's", "'it'\\''s'"},
		// Multiple single quotes.
		{"can't won't", "'can'\\''t won'\\''t'"},
		// Shell metacharacters should be safely quoted.
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		// Backtick substitution.
		{"`rm -rf /`", "'`rm -rf /`'"},
		// Double quotes within single quotes are literal.
		{`say "hello"`, `'say "hello"'`},
		// Semicolons and pipes.
		{"cmd; rm -rf /", "'cmd; rm -rf /'"},
		{"cmd | cat", "'cmd | cat'"},
		// Newlines.
		{"line1\nline2", "'line1\nline2'"},
		// Ampersands.
		{"a && b", "'a && b'"},
		// Hash / comment.
		{"fix bug #42", "'fix bug #42'"},
		// Dollar sign without parens.
		{"costs $5", "'costs $5'"},
	}

	for _, tc := range tests {
		got := Quote(tc.input)
		if got != tc.want {
			t.Errorf("Quote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
