package runner

import (
	"fmt"
	"os/exec"
	"strings"
)

// ReleaseBeads releases in_progress beads labeled pipeline/{session} back to
// open status. This is called on session crash, kill, or stale lock cleanup.
// Uses the bd CLI for bead management. Best-effort: errors are silently
// ignored so callers are never blocked by bead cleanup failures.
//
// bdPath is the path to the bd binary. If empty, "bd" is resolved via PATH.
// If bd is not found, the function returns nil (idempotent).
func ReleaseBeads(session string, bdPath string) error {
	if strings.TrimSpace(session) == "" {
		return nil
	}

	bd := bdPath
	if bd == "" {
		bd = "bd"
	}

	// Resolve bd binary; skip cleanup if bd is not installed.
	resolved, lookErr := exec.LookPath(bd)
	if lookErr != nil {
		return nil //nolint: bd not available, nothing to clean up
	}
	bd = resolved

	label := fmt.Sprintf("pipeline/%s", session)

	// List in_progress beads with the session label.
	out, listErr := exec.Command(bd, "list", "--status", "in_progress", "--label", label, "--quiet").Output()
	if listErr != nil {
		return nil //nolint: best-effort cleanup
	}

	ids := parseBeadIDs(string(out))
	for _, id := range ids {
		_ = exec.Command(bd, "update", id, "--status=open").Run()
	}

	return nil
}

// parseBeadIDs extracts bead IDs from bd list output (one per line).
func parseBeadIDs(output string) []string {
	var ids []string
	for _, line := range strings.Split(output, "\n") {
		id := strings.TrimSpace(line)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
