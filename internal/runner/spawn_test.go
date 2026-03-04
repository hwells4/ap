package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/session"
)

func TestRun_SpawnSignalSuccessEmitsEvent(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-success")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	prov := mock.New(
		mock.WithResponses(mock.Response{
			StatusJSON: `{
        "decision":"stop",
        "reason":"done",
        "summary":"spawn child",
        "work":{"items_completed":[],"files_touched":[]},
        "errors":[],
        "agent_signals":{
          "spawn":[{"run":"ralph:2","session":"child-one","context":"focus child"}]
        }
      }`,
		}),
	)

	launcher := &spawnTestLauncher{}
	res, err := Run(context.Background(), Config{
		Session:        "parent-success",
		RunDir:         runDir,
		StageName:      "ralph",
		Provider:       prov,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        root,
		Launcher:       launcher,
		ExecutablePath: "/usr/bin/ap",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Iterations != 1 {
		t.Fatalf("iterations = %d, want 1", res.Iterations)
	}
	if launcher.startCalls != 1 {
		t.Fatalf("launcher start calls = %d, want 1", launcher.startCalls)
	}

	childReqPath := filepath.Join(root, ".ap", "runs", "child-one", "run_request.json")
	req := readJSONMap(t, childReqPath)
	if req["stage"] != "ralph" {
		t.Fatalf("child request stage = %v, want ralph", req["stage"])
	}
	if got, ok := req["iterations"].(float64); !ok || int(got) != 2 {
		t.Fatalf("child request iterations = %v, want 2", req["iterations"])
	}
	if req["context"] != "focus child" {
		t.Fatalf("child request context = %v, want focus child", req["context"])
	}

	evts := readEvents(t, runDir)
	spawnEvents := filterByType(evts, "signal.spawn")
	if len(spawnEvents) != 1 {
		t.Fatalf("signal.spawn event count = %d, want 1", len(spawnEvents))
	}
	data := spawnEvents[0].Data
	if data["child_session"] != "child-one" {
		t.Fatalf("signal.spawn child_session = %v, want child-one", data["child_session"])
	}
}

func TestRun_SpawnSignalFailureDoesNotStopParent(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-failure")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	prov := mock.New(
		mock.WithResponses(
			mock.Response{
				StatusJSON: `{
          "decision":"continue",
          "reason":"next",
          "summary":"bad spawn",
          "work":{"items_completed":[],"files_touched":[]},
          "errors":[],
          "agent_signals":{"spawn":[{"run":"definitely-not-a-stage","session":"child-bad"}]}
        }`,
			},
			mock.StopResponse("done", "stop"),
		),
	)

	launcher := &spawnTestLauncher{}
	res, err := Run(context.Background(), Config{
		Session:        "parent-failure",
		RunDir:         runDir,
		StageName:      "ralph",
		Provider:       prov,
		Iterations:     2,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        root,
		Launcher:       launcher,
		ExecutablePath: "/usr/bin/ap",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", res.Iterations)
	}
	if launcher.startCalls != 0 {
		t.Fatalf("launcher start calls = %d, want 0", launcher.startCalls)
	}

	evts := readEvents(t, runDir)
	failed := filterByType(evts, "signal.spawn.failed")
	if len(failed) != 1 {
		t.Fatalf("signal.spawn.failed count = %d, want 1", len(failed))
	}
	data := failed[0].Data
	if !strings.Contains(data["error"].(string), "parse run spec") {
		t.Fatalf("unexpected spawn failure error: %v", data["error"])
	}
}

func TestRun_SpawnSignalEnforcesLimits(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-limits")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	prov := mock.New(
		mock.WithResponses(mock.Response{
			StatusJSON: `{
        "decision":"stop",
        "reason":"done",
        "summary":"spawn twice",
        "work":{"items_completed":[],"files_touched":[]},
        "errors":[],
        "agent_signals":{
          "spawn":[
            {"run":"ralph","session":"child-a"},
            {"run":"ralph","session":"child-b"}
          ]
        }
      }`,
		}),
	)

	launcher := &spawnTestLauncher{}
	_, err := Run(context.Background(), Config{
		Session:          "parent-limits",
		RunDir:           runDir,
		StageName:        "ralph",
		Provider:         prov,
		Iterations:       1,
		PromptTemplate:   "iteration ${ITERATION}",
		WorkDir:          root,
		Launcher:         launcher,
		ExecutablePath:   "/usr/bin/ap",
		SpawnMaxChildren: 1,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if launcher.startCalls != 1 {
		t.Fatalf("launcher start calls = %d, want 1", launcher.startCalls)
	}

	evts := readEvents(t, runDir)
	spawned := filterByType(evts, "signal.spawn")
	failed := filterByType(evts, "signal.spawn.failed")
	if len(spawned) != 1 {
		t.Fatalf("signal.spawn count = %d, want 1", len(spawned))
	}
	if len(failed) != 1 {
		t.Fatalf("signal.spawn.failed count = %d, want 1", len(failed))
	}
	data := failed[0].Data
	if !strings.Contains(data["error"].(string), "max_child_sessions") {
		t.Fatalf("unexpected max_child_sessions error: %v", data["error"])
	}
}

func TestRun_SpawnSignalDepthLimit(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-depth")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	prov := mock.New(
		mock.WithResponses(mock.Response{
			StatusJSON: `{
        "decision":"stop",
        "reason":"done",
        "summary":"spawn once",
        "work":{"items_completed":[],"files_touched":[]},
        "errors":[],
        "agent_signals":{"spawn":[{"run":"ralph","session":"child-depth"}]}
      }`,
		}),
	)

	launcher := &spawnTestLauncher{}
	_, err := Run(context.Background(), Config{
		Session:        "parent-depth",
		RunDir:         runDir,
		StageName:      "ralph",
		Provider:       prov,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        root,
		Launcher:       launcher,
		SpawnDepth:     3,
		SpawnMaxDepth:  3,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if launcher.startCalls != 0 {
		t.Fatalf("launcher start calls = %d, want 0", launcher.startCalls)
	}

	evts := readEvents(t, runDir)
	failed := filterByType(evts, "signal.spawn.failed")
	if len(failed) != 1 {
		t.Fatalf("signal.spawn.failed count = %d, want 1", len(failed))
	}
	data := failed[0].Data
	if !strings.Contains(data["error"].(string), "max_spawn_depth") {
		t.Fatalf("unexpected max_spawn_depth error: %v", data["error"])
	}
}

type spawnTestLauncher struct {
	startCalls int
}

func (l *spawnTestLauncher) Start(sessionName string, _ []string, _ session.StartOptions) (session.SessionHandle, error) {
	l.startCalls++
	return session.SessionHandle{
		Session: sessionName,
		PID:     7000 + l.startCalls,
		Backend: "stub",
	}, nil
}

func (l *spawnTestLauncher) Kill(string) error { return nil }
func (l *spawnTestLauncher) Available() bool   { return true }
func (l *spawnTestLauncher) Name() string      { return "stub" }

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return decoded
}
