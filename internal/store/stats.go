package store

import (
	"context"
	"fmt"
)

// StageStats holds aggregate usage statistics for a stage.
type StageStats struct {
	StageName       string `json:"stage_name"`
	TotalSessions   int    `json:"total_sessions"`
	TotalIterations int    `json:"total_iterations"`
	Completed       int    `json:"completed"`
	Failed          int    `json:"failed"`
	AvgIterations   float64 `json:"avg_iterations"`
	LastRun         string `json:"last_run"`
}

// GetStageStats aggregates usage statistics per stage from the iterations
// and sessions tables. Returns a map keyed by stage name.
func (s *Store) GetStageStats(ctx context.Context) (map[string]StageStats, error) {
	// Aggregate from iterations table (gives us stage-level counts even
	// within multi-stage pipelines) joined with session status.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			i.stage_name,
			COUNT(DISTINCT i.session_name) AS total_sessions,
			COUNT(*) AS total_iterations,
			COUNT(DISTINCT CASE WHEN s.status = 'completed' THEN i.session_name END) AS completed_sessions,
			COUNT(DISTINCT CASE WHEN s.status = 'failed' THEN i.session_name END) AS failed_sessions,
			MAX(i.started_at) AS last_run
		FROM iterations i
		LEFT JOIN sessions s ON i.session_name = s.name
		WHERE i.stage_name != ''
		GROUP BY i.stage_name
	`)
	if err != nil {
		return nil, fmt.Errorf("store: get stage stats: %w", err)
	}
	defer rows.Close()

	result := make(map[string]StageStats)
	for rows.Next() {
		var st StageStats
		if err := rows.Scan(
			&st.StageName,
			&st.TotalSessions,
			&st.TotalIterations,
			&st.Completed,
			&st.Failed,
			&st.LastRun,
		); err != nil {
			return nil, fmt.Errorf("store: scan stage stats: %w", err)
		}
		if st.TotalSessions > 0 {
			st.AvgIterations = float64(st.TotalIterations) / float64(st.TotalSessions)
		}
		result[st.StageName] = st
	}
	return result, rows.Err()
}
