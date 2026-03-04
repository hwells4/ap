package session

import "errors"

var (
	// ErrSessionExists is returned when starting a session that already exists.
	ErrSessionExists = errors.New("session: already exists")
	// ErrSessionNotFound is returned when killing a session that doesn't exist.
	ErrSessionNotFound = errors.New("session: not found")
	// ErrStartTimeout is returned when a session doesn't signal readiness in time.
	ErrStartTimeout = errors.New("session: startup timeout")
	// ErrLauncherUnavailable is returned when the launcher backend is not available.
	ErrLauncherUnavailable = errors.New("session: launcher unavailable")
)

// SessionHandle describes a running session returned by Start.
type SessionHandle struct {
	// Session is the session name.
	Session string
	// PID is the process ID of the runner (0 if unknown).
	PID int
	// Backend identifies the launcher type (e.g., "tmux", "process").
	Backend string
}

// StartOptions configures session launch behavior.
type StartOptions struct {
	// WorkDir is the working directory for the runner process.
	WorkDir string
	// Env contains additional environment variables.
	Env map[string]string
	// TimeoutSeconds is the readiness timeout (0 = default 30s).
	TimeoutSeconds int
}

// Launcher is the interface for background session execution backends.
type Launcher interface {
	// Start launches a runner process for the given session.
	// runnerCmd is the full command to execute (e.g., ["ap", "_run", "--session", "s", "--request", "/path"]).
	// Returns a handle on success, or an error if the session already exists or launch fails.
	Start(session string, runnerCmd []string, opts StartOptions) (SessionHandle, error)

	// Kill terminates a running session. Returns ErrSessionNotFound if no such session.
	// Idempotent: killing an already-dead session is not an error.
	Kill(session string) error

	// Available reports whether this launcher backend is usable.
	Available() bool

	// Name returns the launcher backend name.
	Name() string
}
