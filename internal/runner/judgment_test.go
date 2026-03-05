package runner

import (
	"context"
	"testing"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

func TestRun_JudgmentStopsOnConsensus(t *testing.T) {
	runDir, s := tempSession(t)

	agentProv := mock.New(mock.WithFallback(mock.ContinueResponse("working")))
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
		Store:              s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if res.Status != store.StatusCompleted {
		t.Errorf("status = %v, want completed", res.Status)
	}

	evts := readEvents(t, s, "judgment-consensus")
	verdicts := filterByType(evts, store.TypeJudgeVerdict)
	if len(verdicts) < 1 {
		t.Error("expected at least one judge.verdict event")
	}
}

func TestRun_JudgmentContinueDoesNotStop(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:              s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}
}

func TestRun_JudgmentRespectsMinIterations(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:              s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 5 {
		t.Errorf("iterations = %d, want 5", res.Iterations)
	}
}

func TestRun_JudgmentFallbackToFixed(t *testing.T) {
	runDir, s := tempSession(t)

	agentProv := mock.New(mock.WithResponses(
		mock.ContinueResponse("iter 1"),
		mock.ContinueResponse("iter 2"),
		mock.ContinueResponse("iter 3"),
		mock.StopResponse("done", "finished"),
	))

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
		JudgeMaxRetries:    1,
		Store:              s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 4 {
		t.Errorf("iterations = %d, want 4", res.Iterations)
	}

	evts := readEvents(t, s, "judgment-fallback")
	fallbacks := filterByType(evts, store.TypeJudgeFallback)
	if len(fallbacks) != 1 {
		t.Errorf("judge.fallback events = %d, want 1", len(fallbacks))
	}
}

func TestRun_WithoutJudge_UsesFixedOnly(t *testing.T) {
	runDir, s := tempSession(t)

	agentProv := mock.New(mock.WithFallback(mock.ContinueResponse("working")))

	res, err := Run(context.Background(), Config{
		Session:        "no-judge",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       agentProv,
		Iterations:     3,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}

	evts := readEvents(t, s, "no-judge")
	verdicts := filterByType(evts, store.TypeJudgeVerdict)
	if len(verdicts) != 0 {
		t.Errorf("judge.verdict events = %d, want 0", len(verdicts))
	}
}

func TestRun_JudgmentVerdictEventData(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:              s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "verdict-data")
	verdicts := filterByType(evts, store.TypeJudgeVerdict)

	if len(verdicts) < 2 {
		t.Errorf("verdict events = %d, want >= 2", len(verdicts))
	}
}
