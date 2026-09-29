package postedit

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// goTimeoutOnlyOutput is a real `go test` run in which one package hit go
// test's own -timeout alarm and the other passed: no test failed.
const goTimeoutOnlyOutput = `=== RUN   TestSlow
panic: test timed out after 2s
	running tests:
		TestSlow (2s)
FAIL	ex/a	2.006s
=== RUN   TestOK
--- PASS: TestOK (0.00s)
ok  	ex/b	0.003s
FAIL
`

// nextestTimeoutOnlyOutput is the nextest summary issue #945 reported: two
// tests ended at nextest's own slow-timeout, none failed.
const nextestTimeoutOnlyOutput = `     TIMEOUT [  60.004s] (163/221) forge_lab tests::car_settles
     TIMEOUT [  60.006s] (164/221) forge_lab tests::car_rolls
     Summary [ 181.583s] 164/221 tests run: 162 passed (13 slow), 2 timed out, 236 skipped
     TIMEOUT [  60.004s] (163/221) forge_lab tests::car_settles
     TIMEOUT [  60.006s] (164/221) forge_lab tests::car_rolls
error: test run failed
`

// lastPostEditVerdict is the verdict gate.log holds for root's newest
// post-edit run.
func lastPostEditVerdict(t *testing.T, root string) string {
	t.Helper()
	e, ok := lastSuiteLogEntry(root, time.Hour)
	if !ok {
		t.Fatalf("gate.log holds no suite verdict for %s", root)
	}
	return e.Verdict
}

// TestPostEdit_RunnerTimeoutsOnlyIsATimeoutAndItsTargetedRerunIsAllowed is
// issue #945 end to end: a run whose only non-passes are the runner's own
// per-test timeouts was logged red, and the red then refused the one
// targeted rerun a TIMEOUT sanctions. It must read and log as TIMEOUT, and
// the rerun must go through.
func TestPostEdit_RunnerTimeoutsOnlyIsATimeoutAndItsTargetedRerunIsAllowed(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")

	got := tddtest.Pathless(t, PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), fakeRun(false, goTimeoutOnlyOutput)))
	if !strings.Contains(got, "TIMEOUT") || strings.Contains(got, "outcome=red") {
		t.Fatalf("a run whose only failure is a timeout must report TIMEOUT, not red, got:\n%s", got)
	}
	if !strings.Contains(got, "no test failed") {
		t.Fatalf("the line must say the runner's own timeout ended the run and no test failed, got:\n%s", got)
	}
	if v := lastPostEditVerdict(t, root); v != "timeout" {
		t.Fatalf("gate.log verdict = %q, want timeout", v)
	}
	if d := decideBash(t, "s1", root, "go test -run TestSlow ./..."); d.Action != Allow {
		t.Fatalf("the targeted rerun after a TIMEOUT must be allowed, got %v (reason %q)", d.Action, d.Reason)
	}
}

// TestPostEdit_KilledRunKeepsTheBusyBoxTimeoutLine pins the other half of
// the split: a run the gate itself killed at its deadline still reads as the
// box being busy, not as a runner's per-test timeout.
func TestPostEdit_KilledRunKeepsTheBusyBoxTimeoutLine(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")

	var invoked int
	got := tddtest.Pathless(t, PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), countingTimeoutRun(&invoked)))
	if !strings.Contains(got, "a rerun queues behind") {
		t.Fatalf("a killed run must keep the busy-box TIMEOUT line, got:\n%s", got)
	}
}

// TestJudgeEditResult_DeferredRunnerTimeoutsOnlyIsATimeout is the deferred
// path of issue #945, where the reported run came from: a harvested nextest
// run whose only non-passes are TIMEOUT is logged as timeout, never red.
func TestJudgeEditResult_DeferredRunnerTimeoutsOnlyIsATimeout(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	runner := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "forge_lab"}}

	got := tddtest.Pathless(t, judgeEditResult(runner, "", "", SuiteResult{Output: nextestTimeoutOnlyOutput, Duration: 181583 * time.Millisecond}, root, nil, "", ""))
	if !strings.Contains(got, "TIMEOUT") || strings.Contains(got, "outcome=red") {
		t.Fatalf("a deferred run whose only failures are timeouts must report TIMEOUT, not red, got:\n%s", got)
	}
	if !strings.Contains(got, "TIMEOUT after 182s") {
		t.Fatalf("the line must name the run's own 181.6s, rounded to 182s, got:\n%s", got)
	}
	if v := lastPostEditVerdict(t, root); v != "timeout" {
		t.Fatalf("gate.log verdict = %q, want timeout", v)
	}
}
