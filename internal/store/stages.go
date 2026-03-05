package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// StageInput represents a stage entry stored as JSON in sessions.stages_json.
type StageInput struct {
	Name        string `json:"name"`
	Index       int    `json:"index"`
	Iterations  int    `json:"iterations"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// CreateStages writes the stages array into the session's stages_json field.
func (s *Store) CreateStages(ctx context.Context, sessionName string, stages []StageInput) error {
	data, err := json.Marshal(stages)
	if err != nil {
		return fmt.Errorf("store: marshal stages: %w", err)
	}
	return s.UpdateSession(ctx, sessionName, map[string]any{
		"stages_json": string(data),
	})
}

// CompleteStage marks a stage as completed by updating its completed_at field.
func (s *Store) CompleteStage(ctx context.Context, sessionName string, stageIndex int, completedAt string) error {
	stages, err := s.GetStages(ctx, sessionName)
	if err != nil {
		return err
	}
	found := false
	for i := range stages {
		if stages[i].Index == stageIndex {
			stages[i].CompletedAt = completedAt
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("store: stage index %d not found", stageIndex)
	}
	data, err := json.Marshal(stages)
	if err != nil {
		return fmt.Errorf("store: marshal stages: %w", err)
	}
	return s.UpdateSession(ctx, sessionName, map[string]any{
		"stages_json": string(data),
	})
}

// GetStages reads and parses stages_json from the session.
func (s *Store) GetStages(ctx context.Context, sessionName string) ([]StageInput, error) {
	sess, err := s.GetSession(ctx, sessionName)
	if err != nil {
		return nil, err
	}
	var stages []StageInput
	if err := json.Unmarshal([]byte(sess.StagesJSON), &stages); err != nil {
		return nil, fmt.Errorf("store: unmarshal stages: %w", err)
	}
	return stages, nil
}
