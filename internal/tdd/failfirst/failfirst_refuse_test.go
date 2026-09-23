package failfirst

import (
	"strings"
	"testing"
	"time"
)

// TestFailFirstStage_RefusesWhenTheProofRunOutlivedItsBudget pins issue #561's
// first half: a proof run killed at the stage budget measured NOTHING, and the
// stage used to fold that into "inconclusive (fail-open)" and let the commit
// land. A stage whose whole purpose is to prove a staged test goes RED must
// refuse instead, naming the elapsed time and a command the session can run.
func TestFailFirstStage_RefusesWhenTheProofRunOutlivedItsBudget(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go",
		"package m\n\nimport \"testing\"\n\nfunc TestWidget_returnsOne(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	var ran Runner
	run := func(r Runner, _ string) SuiteResult {
		ran = r
		return SuiteResult{Passed: false, TimedOut: true, Duration: 601 * time.Second}
	}

	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)
	})
	if !res.Blocked {
		t.Fatal("a proof run that was killed proved nothing and must refuse the commit, not let it land")
	}
	if !strings.Contains(res.Message, "601") {
		t.Fatalf("message = %q, want it to name the elapsed seconds of the killed run", res.Message)
	}
	if cmd := cmdString(ran); !strings.Contains(res.Message, cmd) {
		t.Fatalf("message = %q, want it to name the command to re-run (%q)", res.Message, cmd)
	}
	requireLoggedVerdict(t, cfg, "timeout-rejected")
	if text := gateLogText(t, cfg); strings.Contains(text, "fail-open") {
		t.Fatalf("a killed proof must not be logged as a fail-open stand-down:\n%s", text)
	}
}

// TestFailFirstStage_RefusesWhenNoBuildSlotEverCameFree pins #561's second
// half, the cause the first cannot see: the proof waited out the build-lock
// budget and never ran at all. Four lanes sharing two slots turned that into a
// silent pass three times on 2026-09-07. It is a different remedy from a
// killed run — nothing about the tests is known to be slow — so it is refused
// with the command that names what holds the box.
func TestFailFirstStage_RefusesWhenNoBuildSlotEverCameFree(t *testing.T) {
	withIsolatedBuildLock(t)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	write(t, root, "src/widget_test.rs", "#[test]\nfn widget_is_one() { assert_eq!(1, crate::widget::widget()); }\n")
	gitDo(t, root, "add", ".")

	_, release, ok := acquireBuildSlot(resolvedDevTarget(root), time.Second, "cargo test -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the gate target's only build slot")
	}
	defer release()

	suiteRan := false
	run := func(Runner, string) SuiteResult {
		suiteRan = true
		return SuiteResult{Passed: true}
	}

	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"src/widget_test.rs"}, []string{"src/widget.rs"}, run)
	})
	if suiteRan {
		t.Fatal("setup: the proof must never have run — the only build slot is held")
	}
	if !res.Blocked {
		t.Fatal("a proof that never got a build slot proved nothing and must refuse the commit")
	}
	if !strings.Contains(res.Message, "gate status") {
		t.Fatalf("message = %q, want it to name a command that reports what holds the box", res.Message)
	}
	if text := gateLogText(t, cfg); strings.Contains(text, "fail-open") {
		t.Fatalf("a proof that never ran must not be logged as a fail-open stand-down:\n%s", text)
	}
}
