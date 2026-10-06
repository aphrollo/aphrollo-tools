package suite

import (
	"errors"
	"strings"
	"testing"
	"time"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// verdictFor is the one place a stage's raw outcome becomes a GateResult, so
// each outcome kind is pinned to three observable facts: whether it blocks,
// the verdict word gate.log records for it (what `gate stats` counts), and
// the message a session reads. A kind that stops blocking, or logs under
// another kind's word, is a gate defect whichever stage it happens in.
func TestVerdictFor_EachOutcomeKindBlocksLogsAndReportsItsOwnWay(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cases := []struct {
		name        string
		outcome     stageOutcome
		wantBlocked bool
		wantLogged  string // "" = nothing logged at all
		wantMessage string // substring the returned message must carry
	}{
		{"pass", stageOutcome{Kind: outcomePass}, false, "", ""},
		{"skipped", stageOutcome{Kind: outcomeSkipped, Reason: "no go.mod"}, false, "skipped", "skipped (no go.mod)"},
		{"runner missing", stageOutcome{Kind: outcomeRunnerMissing, Reason: "cargo not on PATH"}, false, "runner-missing", "skipped (cargo not on PATH)"},
		{"timeout", stageOutcome{Kind: outcomeTimeout, Message: "did not finish"}, true, "timeout-rejected", "did not finish\n"},
		{"check error", stageOutcome{Kind: outcomeCheckError, Err: errors.New("boom"), Message: "could not run"}, true, "check-error-rejected", "could not run"},
		{"fail", stageOutcome{Kind: outcomeFail, Message: "2 tests failed", Result: SuiteResult{Output: "FAIL TestX\n"}}, true, "vet-blocked", "2 tests failed"},
		{"vacuous", stageOutcome{Kind: outcomeVacuous, Message: "executed zero tests"}, true, "vacuous-rejected", "executed zero tests"},
		{"contention", stageOutcome{Kind: outcomeContention, Reason: "lock held", Message: "retry once it finishes"}, true, "contention-rejected", "retry once it finishes"},
		{"unset kind", stageOutcome{}, true, "unclassified-outcome-rejected", "unclassified stage outcome kind 0"},
		{"unknown kind", stageOutcome{Kind: stageOutcomeKind(999)}, true, "unclassified-outcome-rejected", "unclassified stage outcome kind 999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			root := t.TempDir()

			got := verdictFor("precommit", "vet", root, "go vet ./...", tc.outcome)

			if got.Blocked != tc.wantBlocked {
				t.Errorf("Blocked = %v, want %v (%+v)", got.Blocked, tc.wantBlocked, got)
			}
			if !strings.Contains(got.Message, tc.wantMessage) {
				t.Errorf("Message = %q, want it to carry %q", got.Message, tc.wantMessage)
			}
			if tc.wantLogged == "" {
				if got.Message != "" {
					t.Errorf("a pass carries no message, got %q", got.Message)
				}
				if data, err := tddtest.GateLogBytes(t); err == nil && len(data) > 0 {
					t.Errorf("a pass logged a verdict:\n%s", data)
				}
				return
			}
			requireLoggedVerdict(t, cfg, tc.wantLogged)
		})
	}
}

// A failing stage keeps the bytes of its run, so `gate output` can serve the
// failure without a rerun; the other rejections never spawned a suite whose
// output would be worth keeping.
func TestVerdictFor_AFailRetainsTheRunsOutputForGateOutput(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	res := SuiteResult{Output: "--- FAIL: TestWear\n", Duration: 2 * time.Second}

	verdictFor("precommit", "mechanical", root, "go test ./...", stageOutcome{Kind: outcomeFail, Result: res, Message: "red"})

	text, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("no retained output after a failing stage: %v", err)
	}
	if !strings.HasSuffix(text, suiteOutputSeparator+"\n--- FAIL: TestWear\n") || !strings.Contains(text, "verdict: mechanical-blocked\n") {
		t.Errorf("retained record does not carry the failing run:\n%s", text)
	}
}

// Issue #1241: a check-error-rejected verdict means the gate could not run its
// own check, and the event kept only the verdict, so a retro could not say what
// failed or whether it recurs. The event now carries the error the gate printed.
func TestVerdictFor_ACheckErrorKeepsItsErrorOnTheEventBounded(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	long := "cannot start go: " + strings.Repeat("x", 2000)

	verdictFor("precommit", "fail-first", root, "go test ./a", stageOutcome{Kind: outcomeCheckError, Err: errors.New(long), Message: "could not run"})

	var got []string
	for _, e := range ReadEvents(root) {
		if e.Verdict == "check-error-rejected" && e.Kind == "commit_gate" {
			got = append(got, e.Detail["detail"])
		}
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "cannot start go: xxx") {
		t.Fatalf("detail = %q, want the error text on the one check-error-rejected event", got)
	}
	if n := len([]rune(got[0])); n > 300 {
		t.Errorf("detail holds %d characters, want it bounded at 300", n)
	}
}

// The bound is on the text, not before it: an error of exactly the cap is kept
// whole, and one a character longer is cut with an ellipsis in its place.
func TestBoundedErrorText_KeepsTheCapWholeAndCutsOneMore(t *testing.T) {
	exact := strings.Repeat("y", checkErrorDetailCap)
	if got := boundedErrorText(errors.New(exact)); got != exact {
		t.Errorf("an error of %d characters was changed to %d", checkErrorDetailCap, len([]rune(got)))
	}
	got := boundedErrorText(errors.New(exact + "y"))
	if want := strings.Repeat("y", checkErrorDetailCap-1) + "…"; got != want {
		t.Errorf("one past the cap = %d characters, want the cap with a closing ellipsis", len([]rune(got)))
	}
	if boundedErrorText(nil) != "" {
		t.Error("no error must give no text")
	}
}
