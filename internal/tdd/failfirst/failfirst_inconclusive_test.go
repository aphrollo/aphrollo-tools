package failfirst

import (
	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
	"strings"
	"testing"
	"time"
)

// A fail-first proof the memory cap ended proved nothing: the commit is
// refused, and the refusal says the run ended as OOM-KILLED at the cap
// rather than that it was killed after some seconds.
func TestFailFirstStage_CapKilledProofIsRefusedAsOOMKilled(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { _ = Widget() }\n")
	gitDo(t, root, "add", ".")

	run := func(Runner, string) SuiteResult {
		return SuiteResult{TimedOut: true, Inconclusive: "OOM-KILLED at 11.6 GB", Duration: 30 * time.Second}
	}
	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)
	})
	if !res.Blocked {
		t.Fatal("a proof the cap ended proved nothing and must refuse the commit")
	}
	if !strings.Contains(res.Message, "ended as OOM-KILLED at 11.6 GB") || strings.Contains(res.Message, "killed after") {
		t.Fatalf("message = %q, want the cap kill named and no elapsed-seconds kill", res.Message)
	}
	requireLoggedVerdict(t, cfg, "inconclusive-rejected")
}

// A proof whose checkout of HEAD could not be made measured nothing, and the
// commit it was to judge is not told it passed: the stage says it did not run,
// names git's own words, and logs a verdict of its own. The staged tests are
// real and already pass at HEAD here, which is the very thing the stage exists
// to catch, so the line is the only trace of what went unchecked.
func TestFailFirstStage_ACheckoutThatCannotBeMadeIsNamedNotPassedQuietly(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := greenAtHeadRepo(t)
	// git cannot make a worktree where .git/worktrees is a file, on any OS.
	write(t, root, ".git/worktrees", "not a directory\n")
	run := func(Runner, string) SuiteResult {
		t.Error("the proof ran without the checkout it runs in")
		return SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: passedAtHeadJSON}
	}

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)
	})

	if res.Blocked {
		t.Errorf("a box that cannot make the checkout must not block every commit: %s", res.Message)
	}
	line := tddtest.Pathless(t, stderr)
	for _, want := range []string{"worktree-failed", "NOT RUN", "git worktree add", "fatal:"} {
		if !strings.Contains(line, want) {
			t.Errorf("stderr lacks %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "red-proven") {
		t.Errorf("a proof that never ran was logged as red-proven:\n%s", line)
	}
	requireLoggedVerdict(t, cfg, "inconclusive (worktree-failed, fail-open)")
}
