package precommit

import (
	"path/filepath"
	"strings"
	"testing"
)

// A repo that wants full evidence at its merge used to get `vitest related`
// from the gate and then ran the full suite itself, so every merge ran the
// suite twice (82-88s related, 117s full on one consumer). `premerge-js =
// "full"` in aphrollo.toml makes the merge gate's own run the full suite.

func stageComponentChange(t *testing.T, repo, app string) {
	t.Helper()
	write(t, app, "src/lib/Counter.svelte", "<script>let { count } = $props()</script>\n<p>{count * 2}</p>\n")
	gitDo(t, repo, "add", ".")
}

// Serial: sets the process-wide env var FAKE_VITEST_LOG (makeSvelteKitRepo) and installs a process-wide test override (SetLookNodeForTest).
func TestMechanical_PremergeJsFullRunsTheWholeVitestSuiteNotTheRelatedTests(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetLookNodeForTest(func() (string, error) { return "/opt/node/bin/node", nil }))
	repo, app, _ := makeSvelteKitRepo(t)
	write(t, repo, "aphrollo.toml", "[aphrollo]\npremerge-js = \"full\"\n")
	gitDo(t, repo, "add", "aphrollo.toml")
	stageComponentChange(t, repo, app)

	var seen []Runner
	if res := Mechanical(repo, runsAt(&seen, app)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := "/opt/node/bin/node " + filepath.Join(app, "node_modules", "vitest", "vitest.mjs") + " run"
	if got := runLines(seen); len(got) != 1 || got[0] != want {
		t.Fatalf("the merge gate ran %q, want exactly [%s]", got, want)
	}
}

// A value the gate does not know is refused with the fix, not read as the
// default: a typo would leave the repo believing its merge ran the full suite.
// Serial: sets the process-wide env var FAKE_VITEST_LOG (makeSvelteKitRepo) and installs a process-wide test override (SetLookNodeForTest).
func TestMechanical_PremergeJsWithAnUnknownValueIsRefusedNamingTheValues(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetLookNodeForTest(func() (string, error) { return "/opt/node/bin/node", nil }))
	repo, app, _ := makeSvelteKitRepo(t)
	write(t, repo, "aphrollo.toml", "[aphrollo]\npremerge-js = \"ful\"\n")
	gitDo(t, repo, "add", "aphrollo.toml")
	stageComponentChange(t, repo, app)

	var seen []Runner
	res := Mechanical(repo, runsAt(&seen, app))
	if !res.Blocked || !strings.Contains(res.Message, `premerge-js = "ful"`) || !strings.Contains(res.Message, `"related" or "full"`) {
		t.Fatalf("blocked=%v message=%q, want a refusal naming the value and the two choices", res.Blocked, res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("a suite ran under an unreadable setting: %q", runLines(seen))
	}
}
