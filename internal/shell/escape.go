// Package shell provides safe shell string escaping for use in sh -c commands.
package shell

import "strings"

// Quote wraps s in single quotes with proper escaping so it is safe to embed
// in a POSIX sh -c command. Interior single quotes are replaced with the
// canonical '\'' escape sequence.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
