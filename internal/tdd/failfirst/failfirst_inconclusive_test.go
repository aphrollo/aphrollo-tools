package failfirst

import (
	"strings"
	"testing"
	"time"
)

// A fail-first proof the memory cap ended proved nothing: the commit is
// refused, and the refusal says the run ended as OOM-KILLED at the cap
// rather than that it was killed after some seconds.
func TestFailFirstStage_CapKilledProofIsRefusedAsOOMKilled(t *testing.T) {
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
