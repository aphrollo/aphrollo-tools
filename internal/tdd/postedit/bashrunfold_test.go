package postedit

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
)

// bfoldCalls numbers the calls, for no two share a tool_use_id.
var bfoldCalls atomic.Int64

// bfoldPayload is what the harness sends after a shell call: a PostToolUse for a call
// that exited 0, a PostToolUseFailure ("Exit code N" and the output) for one that did
// not.
func bfoldPayload(t *testing.T, cwd, command, output string, exit int) []byte {
	t.Helper()
	m := map[string]any{
		"session_id": "s-bfold", "cwd": cwd, "tool_name": "Bash", "tool_use_id": fmt.Sprintf("toolu_bfold_%d", bfoldCalls.Add(1)),
		"tool_input": map[string]any{"command": command}, "duration_ms": 0,
	}
	if exit == 0 {
		m["hook_event_name"] = "PostToolUse"
		m["tool_response"] = map[string]any{"stdout": output, "stderr": "", "interrupted": false}
	} else {
		m["hook_event_name"] = "PostToolUseFailure"
		m["error"] = "Exit code " + string(rune('0'+exit)) + "\n" + output
		m["is_interrupt"] = false
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// bfoldEdit records an edit of file and queues its fold, as the edit hook does.
func bfoldEdit(t *testing.T, root, file string) {
	t.Helper()
	id := recordEdit(root, file)
	if id == "" {
		t.Fatalf("setup: the edit of %s was not recorded", file)
	}
	shadow.QueueEditFold(shadow.Source{Root: root, Actor: "s-bfold"}, shadowWorld(), shadow.EditFold{Root: root, Actor: "s-bfold", EditID: id, File: file})
}

// bfoldTree states the tree the next hand-run suite measured.
func bfoldTree(t *testing.T, key string) {
	t.Helper()
	old := worktreeKeyFn
	worktreeKeyFn = func(string) (string, error) { return key, nil }
	t.Cleanup(func() { worktreeKeyFn = old })
}

// bfoldUnit is the unit's record in the lane, after the queue is flushed.
func bfoldUnit(t *testing.T, linked, unit string) kernel.Unit {
	t.Helper()
	oldBudget := shadow.Budget
	shadow.Budget = time.Minute // a loaded box must not turn the record under test into a drop
	t.Cleanup(func() { shadow.Budget = oldBudget })
	shadow.Flush()
	st, err := shadowWorld().Open(linked)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rec, _, err := st.Load(ctx, LaneOf(linked))
	if err != nil {
		t.Fatal(err)
	}
	return rec.Units[unit]
}

func bfoldRunsOnTree(t *testing.T, linked, tree string) []kernel.Verdict {
	t.Helper()
	st, err := shadowWorld().Open(linked)
	if err != nil {
		t.Fatal(err)
	}
	v, found, err := st.ReadVerdict(tree)
	if err != nil || !found {
		t.Fatalf("no verdict file for tree %s: found=%v err=%v", tree, found, err)
	}
	var out []kernel.Verdict
	for _, r := range v.Runs {
		out = append(out, r.Result)
	}
	return out
}

func bfoldSetup(t *testing.T) string {
	t.Helper()
	shadow.Flush() // another test's queued run is not this one's
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	return linked
}

// A go test the agent ran by hand is a run like any other: its red opens the red of
// the unit whose test changed, and its green after the code edit closes it, each on
// the tree it measured and recorded as a verdict of that tree.
func TestFoldBashRun_AGoTestTheAgentRanByHandOpensTheRedAndTheGreenClosesIt(t *testing.T) {
	linked := bfoldSetup(t)
	mustWrite(t, filepath.Join(linked, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	test := filepath.Join(linked, "pkg", "p_test.go")
	src := filepath.Join(linked, "pkg", "p.go")
	mustWrite(t, test, "package pkg\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) { if P() != 2 { t.Fatal(\"no\") } }\n")
	mustWrite(t, src, "package pkg\n\nfunc P() int { return 1 }\n")

	bfoldEdit(t, linked, test)
	bfoldTree(t, "bf01")
	FoldBashRun(bfoldPayload(t, linked, "go test ./pkg", "--- FAIL: TestP (0.00s)\nFAIL\nFAIL\texample.com/m/pkg\t0.01s\n", 1))
	if u := bfoldUnit(t, linked, "pkg"); u.Phase != kernel.PhaseOpen || u.LastReal != kernel.VerdictRed || u.LastRealTree != "bf01" {
		t.Fatalf("unit after the red = %+v, want open on the hand-run red of bf01", u)
	}
	if got := bfoldRunsOnTree(t, linked, "bf01"); len(got) != 1 || got[0] != kernel.VerdictRed {
		t.Errorf("verdicts of bf01 = %v, want the one red run", got)
	}

	mustWrite(t, src, "package pkg\n\nfunc P() int { return 2 }\n")
	bfoldEdit(t, linked, src)
	bfoldTree(t, "bf02")
	FoldBashRun(bfoldPayload(t, linked, "go test ./pkg", "ok  \texample.com/m/pkg\t0.01s\n", 0))
	u := bfoldUnit(t, linked, "pkg")
	if u.Phase != kernel.PhaseClosed || u.LastReal != kernel.VerdictGreen || u.LastRealTree != "bf02" {
		t.Errorf("unit after the green = %+v, want closed on the hand-run green of bf02", u)
	}
	if got := bfoldRunsOnTree(t, linked, "bf02"); len(got) != 1 || got[0] != kernel.VerdictGreen {
		t.Errorf("verdicts of bf02 = %v, want the one green run", got)
	}
}

// The same fold for a pytest and a vitest run: the project is the unit.
func TestFoldBashRun_APytestAndAVitestRunTheAgentRanByHandAreTheRunsOfTheirProjects(t *testing.T) {
	linked := bfoldSetup(t)
	mustWrite(t, filepath.Join(linked, "backend", "pyproject.toml"), "[project]\nname = \"backend\"\n")
	mustWrite(t, filepath.Join(linked, "backend", "tests", "test_a.py"), "def test_a():\n    assert 1 == 2\n")
	mustWrite(t, filepath.Join(linked, "frontend", "package.json"), `{"name": "frontend", "devDependencies": {"vitest": "3.2.7"}}`)
	mustWrite(t, filepath.Join(linked, "frontend", "src", "api.test.ts"), "import { test, expect } from 'vitest'\ntest('a', () => expect(1).toBe(2))\n")
	py := filepath.Join(linked, "backend", "tests", "test_a.py")
	ts := filepath.Join(linked, "frontend", "src", "api.test.ts")

	bfoldEdit(t, filepath.Join(linked, "backend"), py)
	bfoldEdit(t, filepath.Join(linked, "frontend"), ts)
	bfoldTree(t, "bf03")
	// The python run is started from the repository root, in the project by `cd`.
	FoldBashRun(bfoldPayload(t, linked, "cd backend && python -m pytest -q", "FAILED tests/test_a.py::test_a - assert 1 == 2\n1 failed in 0.02s\n", 1))
	FoldBashRun(bfoldPayload(t, filepath.Join(linked, "frontend"), "npx vitest run", " Test Files  1 passed (1)\n", 0))

	pyUnit, tsUnit := "python:backend", "typescript:frontend"
	if u := bfoldUnit(t, linked, pyUnit); u.LastReal != kernel.VerdictRed || u.Phase != kernel.PhaseOpen {
		t.Errorf("python unit = %+v, want open on the pytest red", u)
	}
	if u := bfoldUnit(t, linked, tsUnit); u.LastReal != kernel.VerdictGreen || u.LastRealTree != "bf03" {
		t.Errorf("typescript unit = %+v, want the vitest green on bf03", u)
	}
}

// What is no verdict folds nothing: a call that is no test run, a pipeline, an
// interrupted run.
func TestFoldBashRun_ACallThatIsNoSuiteVerdictLeavesTheRecordAlone(t *testing.T) {
	linked := bfoldSetup(t)
	mustWrite(t, filepath.Join(linked, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	test := filepath.Join(linked, "pkg", "p_test.go")
	mustWrite(t, test, "package pkg\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n")
	bfoldEdit(t, linked, test)
	bfoldTree(t, "bf04")
	for _, cmd := range []string{"go build ./...", "go test ./pkg | tail -3", "ls"} {
		FoldBashRun(bfoldPayload(t, linked, cmd, "FAIL\n", 1))
	}
	if u := bfoldUnit(t, linked, "pkg"); u.Phase != kernel.PhasePending || u.LastReal != "" {
		t.Errorf("unit = %+v, want it still pending, with no verdict", u)
	}
}
