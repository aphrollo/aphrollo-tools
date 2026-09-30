package precommit

import (
	"strings"
	"testing"
)

// manifestRepo is a committed Go repo that carries the tddsplit manifest and
// one file under internal/tdd.
func manifestRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "tools/tddsplit/manifest.txt", "# manifest\n")
	write(t, root, "internal/tdd/old.go", "package tdd\n")
	write(t, root, "internal/tdd/keep.go", "package tdd\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "seed")
	return root
}

type recordedRun struct {
	runs   []Runner
	result SuiteResult
}

func (r *recordedRun) run(runner Runner, _ string) SuiteResult {
	r.runs = append(r.runs, runner)
	return r.result
}

// Issue #995: a commit that adds, deletes or renames a file under
// internal/tdd runs the manifest drift test, so a file with no manifest row
// is refused here instead of by CI.
func TestTddsplitManifestStage_RunsTheDriftTestWhenTheFileSetChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"added", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/new.go", "package suite\n")
		}},
		{"deleted", func(t *testing.T, root string) {
			gitDo(t, root, "rm", "-q", "internal/tdd/old.go")
		}},
		{"renamed", func(t *testing.T, root string) {
			gitDo(t, root, "mv", "internal/tdd/old.go", "internal/tdd/renamed.go")
		}},
		{"renamed out of the tree", func(t *testing.T, root string) {
			gitDo(t, root, "mv", "internal/tdd/old.go", "elsewhere.go")
		}},
		{"renamed into the tree", func(t *testing.T, root string) {
			write(t, root, "elsewhere.go", "package x\n")
			gitDo(t, root, "add", "elsewhere.go")
			gitDo(t, root, "commit", "-q", "-m", "outside file")
			gitDo(t, root, "mv", "elsewhere.go", "internal/tdd/arrived.go")
		}},
		{"a path with a space", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/with space.go", "package tdd\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := manifestRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{Passed: true}}
			if res := tddsplitManifestStage("precommit", root, rec.run); res.Blocked {
				t.Fatalf("a green drift test blocked: %s", res.Message)
			}
			if len(rec.runs) != 1 || !strings.Contains(strings.Join(rec.runs[0].Args, " "), "./tools/tddsplit") ||
				!strings.Contains(strings.Join(rec.runs[0].Args, " "), tddsplitDriftTest) {
				t.Fatalf("runs = %v, want one go test of ./tools/tddsplit -run %s", rec.runs, tddsplitDriftTest)
			}
		})
	}
}

func TestTddsplitManifestStage_BlocksOnAFailingDriftTest(t *testing.T) {
	t.Parallel()
	root := manifestRepo(t)
	write(t, root, "internal/tdd/suite/new.go", "package suite\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Output: "the manifest does not map: internal/tdd/suite/new.go\n"}}
	res := tddsplitManifestStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/tdd/suite/new.go") {
		t.Fatalf("result = %+v, want a block quoting the unmapped file", res)
	}
}

// go test exits 0 when -run matches nothing: a renamed drift test must not
// turn the stage into a pass.
func TestTddsplitManifestStage_BlocksWhenTheDriftTestNoLongerExists(t *testing.T) {
	t.Parallel()
	root := manifestRepo(t)
	write(t, root, "internal/tdd/suite/new.go", "package suite\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Passed: true, Output: "testing: warning: no tests to run\nok\n"}}
	res := tddsplitManifestStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "ran no test") {
		t.Fatalf("result = %+v, want a block saying no test ran", res)
	}
}

func TestTddsplitManifestStage_SkipsWhatCannotMoveTheFileSet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"an edit to an existing file", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/keep.go", "package tdd\n\nvar X = 1\n")
		}},
		{"a new file outside internal/tdd", func(t *testing.T, root string) {
			write(t, root, "internal/cli/new.go", "package cli\n")
		}},
		{"nothing staged", func(t *testing.T, root string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := manifestRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{}}
			if res := tddsplitManifestStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
				t.Fatalf("blocked=%v runs=%v, want no run and no block", res.Blocked, rec.runs)
			}
		})
	}
}

func TestTddsplitManifestStage_SkipsARepoWithoutTheManifest(t *testing.T) {
	t.Parallel()
	root := makeGoRepo(t)
	write(t, root, "internal/tdd/new.go", "package tdd\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{}
	if res := tddsplitManifestStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
		t.Fatalf("blocked=%v runs=%v, want no run in a repo with no manifest", res.Blocked, rec.runs)
	}
	if res := tddsplitManifestStage("precommit", "", rec.run); res.Blocked || len(rec.runs) != 0 {
		t.Fatalf("an empty repo root ran the check")
	}
}

// The stage is wired into the commit gate: a commit staging only a deleted
// file (no source, no test) still reaches it.
func TestPrecommitDecide_RefusesADeletionThatLeavesAManifestRow(t *testing.T) {
	t.Parallel()
	root := manifestRepo(t)
	gitDo(t, root, "rm", "-q", "internal/tdd/old.go")
	rec := &recordedRun{result: SuiteResult{Output: "manifest row maps no file: old.go\n"}}
	res := precommitDecide(root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "old.go") {
		t.Fatalf("precommitDecide = %+v, want the drift test's block", res)
	}
}
