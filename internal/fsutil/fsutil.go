// Package fsutil provides shared filesystem helpers for runtime packages.
package fsutil

import "os"

// FileExists reports whether path exists and is a regular file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
