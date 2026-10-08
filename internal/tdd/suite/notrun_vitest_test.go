package suite

import (
	"strings"
	"testing"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// An npm root's commit gate stands down on its suite, and its NOT RUN line
// read like a pass for a .svelte component, a fixture or a JSON file: it said
// "package" and "not tested here" and named the file as if it had been
// checked. The line now says the commit did not test it, that the merge gate
// does, and which files the merge will run.
func TestReportSuitesNotRun_AVitestRootSaysTheMergeGateTestsTheFiles(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	files := []string{"src/lib/Counter.svelte", "src/lib/data.json"}
	runner := Runner{Cmd: "npx", Args: []string{"vitest", "related", files[0], files[1], "--run"}}

	stderr := tddtest.CaptureStderr(t, func() { reportSuitesNotRun("precommit", root, "package", runner, files) })

	want := "NOT RUN — not tested at commit — the merge gate tests it: src/lib/Counter.svelte, src/lib/data.json; this pass is not a green for them"
	if !strings.Contains(stderr, want) {
		t.Errorf("the line lacks %q:\n%s", want, stderr)
	}
}

// A wide JS commit is a count and one example, as a Go one is.
func TestReportSuitesNotRun_AWideVitestListIsACountAndOneFile(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	files := []string{"a.ts", "b.ts", "c.ts", "d.ts"}
	runner := Runner{Cmd: "npx", Args: append([]string{"vitest", "related"}, append(files, "--run")...)}

	stderr := tddtest.CaptureStderr(t, func() { reportSuitesNotRun("precommit", root, "package", runner, files) })

	want := "NOT RUN — not tested at commit — the merge gate tests it: 4 files (e.g. a.ts; the whole list is on the gate log's event, `aphrollo why`); this pass is not a green for them"
	if !strings.Contains(stderr, want) {
		t.Errorf("the line lacks %q:\n%s", want, stderr)
	}
}
