package precommit

import (
	"strings"
	"testing"
)

// callsiteRepo is a committed Go repo that carries the argvbatch call-site
// guard and one non-test source file.
func callsiteRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "internal/argvbatch/callsites_test.go", "package argvbatch\n")
	write(t, root, "internal/cli/tool.go", "package cli\n\nfunc run(paths []string) {\n\texec.Command(\"git\", paths...)\n}\n")
	write(t, root, "internal/cli/plain.go", "package cli\n\nvar X = 1\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "seed")
	return root
}

// Issue #1034: a commit that adds or changes an exec call in a non-test Go
// file runs the argvbatch call-site guard, so a call with no row in
// spreadCallSites is refused here instead of by CI.
func TestCallsiteGuardStage_RunsTheGuardWhenAnExecCallChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"a new exec.Command", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/gitx/new.go", "package gitx\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
		}},
		{"a new git helper call", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/gitx/new.go", "package gitx\n\nfunc a(p []string) { gitRead(\"r\", p...) }\n")
		}},
		{"a new gh helper call", func(t *testing.T, root string) {
			write(t, root, "internal/ciwhy/new.go", "package ciwhy\n\nfunc a(p []string) { runGh(p...) }\n")
		}},
		{"an edited exec call", func(t *testing.T, root string) {
			write(t, root, "internal/cli/tool.go", "package cli\n\nfunc run(paths []string) {\n\texec.CommandContext(nil, \"git\", paths...)\n}\n")
		}},
		{"a deleted exec call", func(t *testing.T, root string) {
			write(t, root, "internal/cli/tool.go", "package cli\n\nfunc run(paths []string) {\n}\n")
		}},
		{"a deleted file holding an exec call", func(t *testing.T, root string) {
			gitDo(t, root, "rm", "-q", "internal/cli/tool.go")
		}},
		{"a path with a space", func(t *testing.T, root string) {
			write(t, root, "internal/cli/with space.go", "package cli\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := callsiteRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{Passed: true}}
			if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked {
				t.Fatalf("a green guard blocked: %s", res.Message)
			}
			if len(rec.runs) != 1 {
				t.Fatalf("runs = %v, want one", rec.runs)
			}
			args := strings.Join(rec.runs[0].Args, " ")
			if want := "test -count=1 ./internal/argvbatch -run ^" + callsiteGuardTest + "$"; args != want {
				t.Fatalf("args = %q, want %q", args, want)
			}
		})
	}
}

func TestCallsiteGuardStage_BlocksOnAFailingGuard(t *testing.T) {
	t.Parallel()
	root := callsiteRepo(t)
	write(t, root, "internal/cli/new.go", "package cli\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Output: "internal/cli/new.go:a hands a slice to a process and is not accounted for\n"}}
	res := callsiteGuardStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/cli/new.go:a") {
		t.Fatalf("result = %+v, want a block quoting the unaccounted call site", res)
	}
}

// go test exits 0 when -run matches nothing: a renamed guard test must not
// turn the stage into a pass.
func TestCallsiteGuardStage_BlocksWhenTheGuardNoLongerExists(t *testing.T) {
	t.Parallel()
	root := callsiteRepo(t)
	write(t, root, "internal/cli/new.go", "package cli\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Passed: true, Output: "testing: warning: no tests to run\nok\n"}}
	res := callsiteGuardStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "ran no test") {
		t.Fatalf("result = %+v, want a block saying no test ran", res)
	}
}

func TestCallsiteGuardStage_SkipsWhatCannotMoveACallSite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"an edit with no exec call", func(t *testing.T, root string) {
			write(t, root, "internal/cli/plain.go", "package cli\n\nvar X = 2\n")
		}},
		{"an exec call in a test file", func(t *testing.T, root string) {
			write(t, root, "internal/cli/new_test.go", "package cli\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
		}},
		{"an exec call in a generated file", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/gitx/export.go", "package gitx\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
		}},
		{"an exec call in a non-Go file", func(t *testing.T, root string) {
			write(t, root, "README.md", "exec.Command(\"git\", p...)\n")
		}},
		{"nothing staged", func(t *testing.T, root string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := callsiteRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{}}
			if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
				t.Fatalf("blocked=%v runs=%v, want no run and no block", res.Blocked, rec.runs)
			}
		})
	}
}

func TestCallsiteGuardStage_SkipsARepoWithoutTheGuard(t *testing.T) {
	t.Parallel()
	root := makeGoRepo(t)
	write(t, root, "internal/cli/new.go", "package cli\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{}
	if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
		t.Fatalf("blocked=%v runs=%v, want no run in a repo with no guard", res.Blocked, rec.runs)
	}
	if res := callsiteGuardStage("precommit", "", rec.run); res.Blocked || len(rec.runs) != 0 {
		t.Fatalf("an empty repo root ran the check")
	}
}

// The stage is wired into the commit gate.
func TestPrecommitDecide_RefusesANewExecCallTheGuardDoesNotList(t *testing.T) {
	t.Parallel()
	root := callsiteRepo(t)
	write(t, root, "internal/cli/new.go", "package cli\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Output: "internal/cli/new.go:a is not accounted for\n"}}
	res := precommitDecide(root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/cli/new.go:a") {
		t.Fatalf("precommitDecide = %+v, want the guard's block", res)
	}
}

func TestDiffChangesExecCall_JudgesOnlyChangedLinesOfSourceFiles(t *testing.T) {
	t.Parallel()
	hunk := func(path, line string) string {
		return "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n@@ -1 +1 @@\n" + line + "\n"
	}
	for _, tc := range []struct {
		name string
		diff string
		want bool
	}{
		{"added exec.Command", hunk("a.go", "+\texec.Command(\"x\")"), true},
		{"removed exec.CommandContext", hunk("a.go", "-\texec.CommandContext(ctx, \"x\")"), true},
		{"added Git wrapper", hunk("a.go", "+\tout, _ := Git(dir, args...)"), true},
		{"added lower git wrapper", hunk("a.go", "+\tout, _ := git(dir, args...)"), true},
		{"added gh wrapper", hunk("a.go", "+\tghOutput(args...)"), true},
		{"added runGit wrapper", hunk("a.go", "+\trunGitCapture(dir, args...)"), true},
		{"a context line is not a change", hunk("a.go", " \texec.Command(\"x\")"), false},
		{"a file header naming exec.Command is not a change", "diff --git a/exec.Command( b/exec.Command(\n--- a/exec.Command(\n+++ b/exec.Command(\n@@ -1 +1 @@\n+x := 1\n", false},
		{"an unrelated added line", hunk("a.go", "+\tfmt.Println(1)"), false},
		{"a word merely containing git", hunk("a.go", "+\tdigit(1)"), false},
		{"a test file", hunk("a_test.go", "+\texec.Command(\"x\")"), false},
		{"a generated export.go", hunk("export.go", "+\texec.Command(\"x\")"), false},
		{"a generated deps_ file", hunk("deps_gitx.go", "+\tGit(a, b...)"), false},
		{"a generated api_ file", hunk("api_x.go", "+\tGit(a, b...)"), false},
		{"a non-Go file", hunk("a.md", "+exec.Command(\"x\")"), false},
		{"empty diff", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := execGuard.diffChanges(tc.diff); got != tc.want {
				t.Fatalf("execGuard.diffChanges = %v, want %v", got, tc.want)
			}
		})
	}
}

// A deleted file shows its path only on the --- side, and an added file only
// on the +++ side; either must still be attributed to its file.
func TestDiffChangesExecCall_AttributesAddedAndDeletedFiles(t *testing.T) {
	t.Parallel()
	added := "diff --git a/n.go b/n.go\nnew file mode 100644\n--- /dev/null\n+++ b/n.go\n@@ -0,0 +1 @@\n+\texec.Command(\"x\")\n"
	deleted := "diff --git a/n.go b/n.go\ndeleted file mode 100644\n--- a/n.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-\texec.Command(\"x\")\n"
	addedTest := strings.ReplaceAll(added, "n.go", "n_test.go")
	deletedTest := strings.ReplaceAll(deleted, "n.go", "n_test.go")
	for name, tc := range map[string]struct {
		diff string
		want bool
	}{"added": {added, true}, "deleted": {deleted, true}, "added test": {addedTest, false}, "deleted test": {deletedTest, false}} {
		if got := execGuard.diffChanges(tc.diff); got != tc.want {
			t.Errorf("%s: execGuard.diffChanges = %v, want %v", name, got, tc.want)
		}
	}
}

// A removed line that begins with "-- " looks like a --- header; inside a
// hunk it is content, and the file it belongs to stays the one the header named.
func TestDiffChangesExecCall_ReadsHeaderLookalikesInsideAHunkAsContent(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,2 @@\n--- a/other_test.go\n+++ b/other_test.go\n+\texec.Command(\"x\")\n"
	if !execGuard.diffChanges(diff) {
		t.Fatal("an exec call added to a.go after a header lookalike was not seen")
	}
}

// relatedRepo is a callsiteRepo that also carries the related-runner guard and
// one non-test file in the gate's own tree that names a related-tests verb.
func relatedRepo(t *testing.T) string {
	t.Helper()
	root := callsiteRepo(t)
	write(t, root, "internal/argvbatch/relatedsites_test.go", "package argvbatch\n")
	write(t, root, "internal/tdd/postedit/touched.go", "package postedit\n\nfunc fileArgPos(a []string) int {\n\tif a[1] == \"related\" {\n\t\treturn 2\n\t}\n\treturn -1\n}\n")
	write(t, root, "internal/tdd/postedit/plain.go", "package postedit\n\nvar X = 1\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "seed related")
	return root
}

// relatedGuardArgs is the one command the stage runs for the related-runner
// table: its test, by name, in the argvbatch package.
const relatedGuardArgs = "test -count=1 ./internal/argvbatch -run ^TestRelatedRunnerSites_EveryBuilderOfARelatedRunIsBounded$"

// Issue #1079, the escape from PR #1077: a commit that adds a function naming a
// related-tests verb to the gate's own source holds no exec call, so the stage
// ran nothing, and the missing relatedRunnerSites row failed only in CI.
func TestCallsiteGuardStage_RunsTheRelatedRunnerGuardWhenAVerbIsNamed(t *testing.T) {
	t.Parallel()
	touched := "package postedit\n\nfunc fileArgPos(a []string) int {\n\tif a[1] == \"related\" {\n\t\treturn 2\n\t}\n\treturn -1\n}\n"
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"a new helper naming the vitest verb", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc isVitest(a []string) bool { return a[1] == \"related\" }\n")
		}},
		{"a new helper naming the jest verb", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc isJest(a []string) bool { return a[1] == \"--findRelatedTests\" }\n")
		}},
		{"a new function in a file that already names the verb", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/touched.go", touched+"\nfunc fileArgEnd(a []string) bool {\n\treturn a[1] == \"related\"\n}\n")
		}},
		{"a raw-string verb", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc isVitest(a []string) bool { return a[1] == `related` }\n")
		}},
		{"a deleted verb line", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/touched.go", "package postedit\n\nfunc fileArgPos(a []string) int {\n\treturn -1\n}\n")
		}},
		{"a deleted file naming the verb", func(t *testing.T, root string) {
			gitDo(t, root, "rm", "-q", "internal/tdd/postedit/touched.go")
		}},
		{"a file the exec guard skips as generated", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/export.go", "package postedit\n\nfunc isVitest(a []string) bool { return a[1] == \"related\" }\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := relatedRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{Passed: true}}
			if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked {
				t.Fatalf("a green guard blocked: %s", res.Message)
			}
			if len(rec.runs) != 1 {
				t.Fatalf("runs = %v, want one", rec.runs)
			}
			if args := strings.Join(rec.runs[0].Args, " "); args != relatedGuardArgs {
				t.Fatalf("args = %q, want %q", args, relatedGuardArgs)
			}
		})
	}
}

func TestCallsiteGuardStage_SkipsWhatCannotMoveARelatedRunnerRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"an edit naming no verb", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/plain.go", "package postedit\n\nvar X = 2\n")
		}},
		{"the verb in a test file", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/new_test.go", "package postedit\n\nvar v = \"related\"\n")
		}},
		{"the verb outside the gate's tree", func(t *testing.T, root string) {
			write(t, root, "internal/cli/new.go", "package cli\n\nvar v = \"related\"\n")
		}},
		{"the verb under a tddtest directory", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/internal/tddtest/new.go", "package tddtest\n\nvar v = \"related\"\n")
		}},
		{"the verb under a gotmp directory", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/gotmp1/new.go", "package gotmp1\n\nvar v = \"related\"\n")
		}},
		{"the verb in a non-Go file", func(t *testing.T, root string) {
			write(t, root, "README.md", "run vitest \"related\"\n")
		}},
		{"a longer word", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nvar v = \"relatedness\"\n")
		}},
		{"nothing staged", func(t *testing.T, root string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := relatedRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{}}
			if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
				t.Fatalf("blocked=%v runs=%v, want no run and no block", res.Blocked, rec.runs)
			}
		})
	}
}

func TestCallsiteGuardStage_SkipsARepoWithoutTheRelatedRunnerTable(t *testing.T) {
	t.Parallel()
	root := callsiteRepo(t)
	write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nvar v = \"related\"\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{}
	if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
		t.Fatalf("blocked=%v runs=%v, want no run in a repo with no related-runner table", res.Blocked, rec.runs)
	}
}

// One commit that changes an exec call and names a verb answers to both
// tables, the exec table first.
func TestCallsiteGuardStage_RunsEachGuardTheDiffMoves(t *testing.T) {
	t.Parallel()
	root := relatedRepo(t)
	write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc a(p []string) bool {\n\texec.Command(\"git\", p...)\n\treturn p[1] == \"related\"\n}\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Passed: true}}
	if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked {
		t.Fatalf("a green guard blocked: %s", res.Message)
	}
	var got []string
	for _, r := range rec.runs {
		got = append(got, strings.Join(r.Args, " "))
	}
	want := []string{"test -count=1 ./internal/argvbatch -run ^TestExecCallSites_EverySpreadArgumentListIsBatchedOrBounded$", relatedGuardArgs}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("runs = %q, want %q", got, want)
	}
}

func TestCallsiteGuardStage_BlocksOnAFailingRelatedRunnerGuard(t *testing.T) {
	t.Parallel()
	root := relatedRepo(t)
	write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc isVitest(a []string) bool { return a[1] == \"related\" }\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Output: "internal/tdd/postedit/new.go:isVitest names a related-tests verb and is not accounted for\n"}}
	res := callsiteGuardStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/tdd/postedit/new.go:isVitest") {
		t.Fatalf("result = %+v, want a block quoting the unaccounted builder", res)
	}
}

// The exec guard passing does not excuse the related-runner guard.
func TestCallsiteGuardStage_BlocksWhenOnlyTheRelatedRunnerGuardFails(t *testing.T) {
	t.Parallel()
	root := relatedRepo(t)
	write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc a(p []string) bool {\n\texec.Command(\"git\", p...)\n\treturn p[1] == \"related\"\n}\n")
	gitDo(t, root, "add", "-A")
	run := func(r Runner, _ string) SuiteResult {
		if strings.Contains(strings.Join(r.Args, " "), "TestRelatedRunnerSites") {
			return SuiteResult{Output: "internal/tdd/postedit/new.go:a names a related-tests verb and is not accounted for\n"}
		}
		return SuiteResult{Passed: true}
	}
	res := callsiteGuardStage("precommit", root, run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/tdd/postedit/new.go:a") {
		t.Fatalf("result = %+v, want the related-runner guard's block", res)
	}
}

// go test exits 0 when -run matches nothing: a renamed related-runner test must
// not turn the stage into a pass.
func TestCallsiteGuardStage_BlocksWhenTheRelatedRunnerGuardNoLongerExists(t *testing.T) {
	t.Parallel()
	root := relatedRepo(t)
	write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc isVitest(a []string) bool { return a[1] == \"related\" }\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Passed: true, Output: "testing: warning: no tests to run\nok\n"}}
	res := callsiteGuardStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "TestRelatedRunnerSites_EveryBuilderOfARelatedRunIsBounded") {
		t.Fatalf("result = %+v, want a block naming the related-runner test that ran nothing", res)
	}
}

// The related-runner guard is wired into the commit gate through the stage.
func TestPrecommitDecide_RefusesANewRelatedRunnerTheTableDoesNotList(t *testing.T) {
	t.Parallel()
	root := relatedRepo(t)
	write(t, root, "internal/tdd/postedit/new.go", "package postedit\n\nfunc isVitest(a []string) bool { return a[1] == \"related\" }\n")
	gitDo(t, root, "add", "-A")
	run := func(r Runner, _ string) SuiteResult {
		if strings.Contains(strings.Join(r.Args, " "), "TestRelatedRunnerSites") {
			return SuiteResult{Output: "internal/tdd/postedit/new.go:isVitest is not accounted for\n"}
		}
		return SuiteResult{Passed: true}
	}
	res := precommitDecide(root, run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/tdd/postedit/new.go:isVitest") {
		t.Fatalf("precommitDecide = %+v, want the related-runner guard's block", res)
	}
}
