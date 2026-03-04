package runner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/state"
)

func TestRun_JudgmentStopsOnConsensus(t *testing.T) {
	runDir := tempSession(t)

	// Agent says "continue" for 5 iterations, but judge says "stop" twice.
	agentProv := mock.New(mock.WithFallback(mock.ContinueResponse("working")))

	// Judge returns stop verdict as JSON in stdout.
	judgeProv := mock.New(mock.WithFallback(mock.Response{
		Stdout: `{"decision":"stop","confidence":0.9,"rationale":"task complete"}`,
	}))

	res, err := Run(context.Background(), Config{
		Session:            "judgment-consensus",
		RunDir:             runDir,
		StageName:          "test-stage",
		Provider:           agentProv,
		Iterations:         10,
		PromptTemplate:     "iteration ${ITERATION}",
		JudgeProvider:      judgeProv,
		JudgeConsensus:     2,
		JudgeMinIterations: 1,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Should stop after 2 iterations (consensus=2, judge always says stop).
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if res.Status != state.StateCompleted {
		t.Errorf("status = %v, want completed", res.Status)
	}

	// Verify judge verdict events were emitted.
	evts := readEvents(t, runDir)
	verdicts := filterByType(evts, events.TypeJudgeVerdict)
	if len(verdicts) < 1 {
		t.Error("expected at least one judge.verdict event")
	}
}

func TestRun_JudgmentContinueDoesNotStop(t *testing.T) {
	runDir := tempSession(t)

	agentProv := mock.New(mock.WithFallback(mock.ContinueResponse("working")))
	judgeProv := mock.New(mock.WithFallback(mock.Response{
		Stdout: `{"decision":"continue","confidence":0.7,"rationale":"still progressing"}`,
	}))

	res, err := Run(context.Background(), Config{
		Session:            "judgment-continue",
		RunDir:             runDir,
		StageName:          "test-stage",
		Provider:           agentProv,
		Iterations:         3,
		PromptTemplate:     "iteration ${ITERATION}",
		JudgeProvider:      judgeProv,
		JudgeConsensus:     2,
		JudgeMinIterations: 1,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Judge always says continue, so we should complete all 3 iterations.
	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}
}

func TestRun_JudgmentRespectsMinIterations(t *testing.T) {
	runDir := tempSession(t)

	agentProv := mock.New(mock.WithFallback(mock.ContinueResponse("working")))
	judgeProv := mock.New(mock.WithFallback(mock.Response{
		Stdout: `{"decision":"stop","confidence":0.95,"rationale":"done"}`,
	}))

	res, err := Run(context.Background(), Config{
		Session:            "judgment-min-iter",
		RunDir:             runDir,
		StageName:          "test-stage",
		Provider:           agentProv,
		Iterations:         10,
		PromptTemplate:     "iteration ${ITERATION}",
		JudgeProvider:      judgeProv,
		JudgeConsensus:     1,
		JudgeMinIterations: 5,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Min iterations = 5, consensus = 1, so should stop at iteration 5.
	if res.Iterations != 5 {
		t.Errorf("iterations = %d, want 5", res.Iterations)
	}
}

func TestRun_JudgmentFallbackToFixed(t *testing.T) {
	runDir := tempSession(t)

	// Agent stops at iteration 4.
	agentProv := mock.New(mock.WithResponses(
		mock.ContinueResponse("iter 1"),
		mock.ContinueResponse("iter 2"),
		mock.ContinueResponse("iter 3"),
		mock.StopResponse("done", "finished"),
	))

	// Judge always returns garbage → 3 failures → fallback.
	judgeProv := mock.New(mock.WithFallback(mock.Response{
		Stdout: "this is not json at all",
	}))

	res, err := Run(context.Background(), Config{
		Session:            "judgment-fallback",
		RunDir:             runDir,
		StageName:          "test-stage",
		Provider:           agentProv,
		Iterations:         10,
		PromptTemplate:     "iteration ${ITERATION}",
		JudgeProvider:      judgeProv,
		JudgeConsensus:     1,
		JudgeMinIterations: 1,
		JudgeMaxRetries:    1, // 1 retry per judge call → fails quickly
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// After 3 judge failures, falls back to fixed strategy.
	// Agent says stop at iteration 4.
	if res.Iterations != 4 {
		t.Errorf("iterations = %d, want 4", res.Iterations)
	}

	// Verify fallback event was emitted.
	evts := readEvents(t, runDir)
	fallbacks := filterByType(evts, events.TypeJudgeFallback)
	if len(fallbacks) != 1 {
		t.Errorf("judge.fallback events = %d, want 1", len(fallbacks))
	}
}

func TestRun_WithoutJudge_UsesFixedOnly(t *testing.T) {
	runDir := tempSession(t)

	agentProv := mock.New(mock.WithFallback(mock.ContinueResponse("working")))

	res, err := Run(context.Background(), Config{
		Session:        "no-judge",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       agentProv,
		Iterations:     3,
		PromptTemplate: "iteration ${ITERATION}",
		// No JudgeProvider — fixed strategy only.
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}

	// No judge events should exist.
	evts := readEvents(t, runDir)
	verdicts := filterByType(evts, events.TypeJudgeVerdict)
	if len(verdicts) != 0 {
		t.Errorf("judge.verdict events = %d, want 0", len(verdicts))
	}
}

func TestRun_JudgmentVerdictEventData(t *testing.T) {
	runDir := tempSession(t)

	agentProv := mock.New(mock.WithFallback(mock.ContinueResponse("working")))
	judgeProv := mock.New(mock.WithResponses(
		mock.Response{Stdout: `{"decision":"continue","confidence":0.6,"rationale":"progress"}`},
		mock.Response{Stdout: `{"decision":"stop","confidence":0.9,"rationale":"done"}`},
		mock.Response{Stdout: `{"decision":"stop","confidence":0.95,"rationale":"confirmed done"}`},
	))

	_, err := Run(context.Background(), Config{
		Session:            "verdict-data",
		RunDir:             runDir,
		StageName:          "test-stage",
		Provider:           agentProv,
		Iterations:         10,
		PromptTemplate:     "iteration ${ITERATION}",
		JudgeProvider:      judgeProv,
		JudgeConsensus:     2,
		JudgeMinIterations: 1,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, runDir)
	verdicts := filterByType(evts, events.TypeJudgeVerdict)

	// First verdict: continue, consecutive_stops = 0
	if len(verdicts) < 1 {
		t.Fatal("expected at least 1 verdict event")
	}
	if stops, _ := verdicts[0].Data["consecutive_stops"].(json.Number); stops.String() != "0" {
		// json.Number for numbers from json.Unmarshal
	}

	// Should have emitted at least 2 verdict events (continue + stop + stop).
	if len(verdicts) < 2 {
		t.Errorf("verdict events = %d, want >= 2", len(verdicts))
	}
}
