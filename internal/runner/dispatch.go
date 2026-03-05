package runner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hwells4/ap/internal/store"
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

// LoadDispatchStateFromStore queries the store for signal events and builds
// a dispatch state.
func LoadDispatchStateFromStore(ctx context.Context, st *store.Store, session string) (*DispatchState, error) {
	ds := NewDispatchState()

	rows, err := st.GetEvents(ctx, session, "", 0)
	if err != nil {
		return nil, fmt.Errorf("dispatch: get events from store: %w", err)
	}

	for _, row := range rows {
		var data map[string]any
		if err := json.Unmarshal([]byte(row.DataJSON), &data); err != nil {
			continue
		}

		signalID, _ := data["signal_id"].(string)
		if signalID == "" {
			continue
		}

		switch row.Type {
		case store.TypeSignalDispatching:
			ds.dispatched[signalID] = true
		case store.TypeSignalSpawn, store.TypeSignalSpawnFailed,
			store.TypeSignalEscalate:
			ds.completed[signalID] = true
		}
	}

	return ds, nil
}
