package store

// Status constants for session lifecycle states.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusAborted   = "aborted"
)

// Event type constants for session lifecycle events.
// These were originally defined in internal/events and are now canonical here.
const (
	// TypeSessionStart marks the start of a session.
	TypeSessionStart = "session.started"
	// TypeSessionComplete marks the completion of a session.
	TypeSessionComplete = "session.completed"
	// TypeNodeStart marks the start of a node.
	TypeNodeStart = "node.started"
	// TypeNodeComplete marks the completion of a node.
	TypeNodeComplete = "node.completed"
	// TypeIterationStart marks the start of an iteration.
	TypeIterationStart = "iteration.started"
	// TypeIterationComplete marks the completion of an iteration.
	TypeIterationComplete = "iteration.completed"
	// TypeIterationFailed marks an iteration failure.
	TypeIterationFailed = "iteration.failed"
	// TypeError marks an error event.
	TypeError = "error"
	// TypeSignalInject marks an inject signal from the agent.
	TypeSignalInject = "signal.inject"
	// TypeSignalEscalate marks an escalation signal from the agent.
	TypeSignalEscalate = "signal.escalate"
	// TypeSignalDispatching marks the start of a two-phase signal side effect.
	TypeSignalDispatching = "signal.dispatching"
	// TypeSignalSpawn marks a successful child session spawn.
	TypeSignalSpawn = "signal.spawn"
	// TypeSignalSpawnFailed marks a failed child session spawn.
	TypeSignalSpawnFailed = "signal.spawn.failed"
	// TypeSignalHandlerError marks a non-fatal signal handler failure.
	TypeSignalHandlerError = "signal.handler.error"
	// TypeIterationRetried marks an iteration retry attempt after failure.
	TypeIterationRetried = "iteration.retried"
	// TypeJudgeVerdict marks a judgment termination evaluation result.
	TypeJudgeVerdict = "judge.verdict"
	// TypeJudgeFallback marks that the judge has entered fallback mode.
	TypeJudgeFallback = "judge.fallback"
)
