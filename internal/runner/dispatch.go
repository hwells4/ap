package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hwells4/ap/internal/events"
)

// SignalID generates a deterministic signal identifier.
// Format: sig-{iteration}-{type}-{index}
func SignalID(iteration int, signalType string, index int) string {
	return fmt.Sprintf("sig-%d-%s-%d", iteration, signalType, index)
}

// DispatchState tracks two-phase signal lifecycle for crash recovery.
// Signals follow: signal.dispatching → side effect → signal.{type} result.
// On crash between dispatching and result, the signal is "in-flight"
// and must be re-dispatched on resume.
type DispatchState struct {
	dispatched map[string]bool // signal_id → has dispatching event
	completed  map[string]bool // signal_id → has result event
}

// NewDispatchState creates an empty dispatch state.
func NewDispatchState() *DispatchState {
	return &DispatchState{
		dispatched: make(map[string]bool),
		completed:  make(map[string]bool),
	}
}

// IsCompleted returns true if the signal has both dispatching and result events.
func (ds *DispatchState) IsCompleted(signalID string) bool {
	return ds.completed[signalID]
}

// IsInFlight returns true if the signal was dispatched but has no result
// (crashed between dispatching and result).
func (ds *DispatchState) IsInFlight(signalID string) bool {
	return ds.dispatched[signalID] && !ds.completed[signalID]
}

// ShouldSkip returns true if the signal is already completed (dispatched + result)
// and should be skipped on resume replay.
func (ds *DispatchState) ShouldSkip(signalID string) bool {
	return ds.completed[signalID]
}

// LoadDispatchState reads events.jsonl and builds a dispatch state that
// identifies which signal dispatches are complete vs in-flight.
func LoadDispatchState(eventsPath string) (*DispatchState, error) {
	ds := NewDispatchState()

	data, err := os.ReadFile(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ds, nil
		}
		return nil, fmt.Errorf("dispatch: read events: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var evt events.Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue // skip malformed lines
		}

		signalID, _ := evt.Data["signal_id"].(string)
		if signalID == "" {
			continue
		}

		switch evt.Type {
		case events.TypeSignalDispatching:
			ds.dispatched[signalID] = true
		case events.TypeSignalSpawn, events.TypeSignalSpawnFailed,
			events.TypeSignalEscalate:
			ds.completed[signalID] = true
		}
	}

	return ds, nil
}

// emitDispatching writes a signal.dispatching event.
func emitDispatching(ew *events.Writer, session string, cursor *events.Cursor, signalID, signalType string, iteration int) error {
	return ew.Append(events.NewEvent(events.TypeSignalDispatching, session, cursor, map[string]any{
		"signal_id":   signalID,
		"signal_type": signalType,
		"iteration":   iteration,
	}))
}
