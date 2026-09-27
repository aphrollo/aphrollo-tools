package precommit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declaredTscLines declares one tsc run for frontend that is judged against
// HEAD's output.
const declaredTscLines = `[aphrollo.precommit]
"frontend" = [
  { argv = ["npx", "tsc", "--noEmit"], baseline = "lines" },
]
`

// oldError is the error HEAD already carried, as a tool names it: under the
// absolute path of the checkout it ran in.
func oldError(checkout string) string {
	return filepath.Join(checkout, "frontend", "src", "a.ts") + "(1,14): error TS2322: old"
}

// headAndStaged is a runner that fails tsc in frontend, printing staged
// there and head in any other checkout's frontend. Every other command, and
// every other directory, passes. The HEAD runs' directories are recorded.
func headAndStaged(frontend string, headDirs *[]string, staged, head func(checkout string) string, headRes SuiteResult) SuiteRunner {
	return func(r Runner, dir string) SuiteResult {
		if !strings.Contains(cmdLine(r), "tsc") || filepath.Base(dir) != "frontend" {
			return SuiteResult{Passed: true}
		}
		checkout := filepath.Dir(dir)
		if dir == frontend {
			return SuiteResult{Output: staged(checkout)}
		}
		*headDirs = append(*headDirs, dir)
		if headRes.TimedOut {
			return headRes
		}
		return SuiteResult{Output: head(checkout)}
	}
}

// A failure HEAD already had is not the commit's to answer for when the
// command asks for a lines baseline. HEAD's run happens in a tree outside the
// root's node_modules whose root links the root's node_modules, so a
// declared npx finds the root's packages, and the tree leaves nothing
// behind. The path each checkout prints and the trailing whitespace differ
// between the two runs, and neither makes the line new.
func TestDeclaredBaseline_AFailureAlreadyAtHeadPassesWithLines(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredTscLines)
	nodeModules := filepath.Join(frontend, "node_modules")
	before := listTree(t, nodeModules)

	var headDirs []string
	reached := map[string]bool{}
	staged := func(c string) string { return "Checking...\n" + oldError(c) + "  \t\n" }
	head := func(c string) string {
		_, err := os.Stat(filepath.Join(c, "frontend", "node_modules", "typescript", "package.json"))
		reached[c] = err == nil
		return "Checking...\n" + oldError(c) + "\n"
	}
	if res := Precommit(repo, headAndStaged(frontend, &headDirs, staged, head, SuiteResult{})); res.Blocked {
		t.Fatalf("a failure already at HEAD refused the commit:\n%s", res.Message)
	}
	if len(headDirs) != 1 || strings.HasPrefix(headDirs[0], nodeModules) || strings.HasPrefix(headDirs[0], evalSymlinks(t, nodeModules)) {
		t.Fatalf("HEAD runs at %v, want one outside %s", headDirs, nodeModules)
	}
	if state := StateDir(); state == "" || !strings.HasPrefix(headDirs[0], state+string(filepath.Separator)) {
		t.Fatalf("HEAD's run at %s is not in the gate state dir %q", headDirs[0], state)
	}
	if !reached[filepath.Dir(headDirs[0])] {
		t.Fatalf("HEAD's run at %s did not reach the root's installed packages", headDirs[0])
	}
	if after := listTree(t, nodeModules); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the root's node_modules changed:\nbefore %q\nafter  %q", before, after)
	}
	if got := gitOut(repo, "worktree", "list"); len(strings.Split(strings.TrimSpace(got), "\n")) != 1 {
		t.Fatalf("the HEAD worktree was left registered:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(frontend, "node_modules", ".aphrollo-head-*")); len(left) != 0 {
		t.Fatalf("the HEAD tree was left on disk: %v", left)
	}
}

// Without a lines baseline, spelled out or left at its default, the same
// failure blocks as it always did, and HEAD is never run.
func TestDeclaredBaseline_WithoutLinesTheSameFailureBlocks(t *testing.T) {
	for _, decl := range []string{
		`[["npx", "tsc", "--noEmit"]]`,
		`[{ argv = ["npx", "tsc", "--noEmit"], baseline = "none" }]`,
		`[{ argv = ["npx", "tsc", "--noEmit"] }]`,
	} {
		repo, frontend := makeFrontendRepo(t, "[aphrollo.precommit]\n\"frontend\" = "+decl+"\n")
		var headDirs []string
		same := func(c string) string { return oldError(c) + "\n" }
		res := Precommit(repo, headAndStaged(frontend, &headDirs, same, same, SuiteResult{}))
		if !res.Blocked || len(headDirs) != 0 {
			t.Errorf("%s: blocked=%v with HEAD runs %v, want a block and no HEAD run", decl, res.Blocked, headDirs)
		}
	}
}

// A line HEAD's run did not print is new, and the refusal quotes it and not
// the lines HEAD had.
func TestDeclaredBaseline_ANewLineIsRefusedAndNamed(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredTscLines)

	var headDirs []string
	staged := func(c string) string { return oldError(c) + "\nsrc/b.ts(3,1): error TS2304: new\n" }
	head := func(c string) string { return oldError(c) + "\n" }
	res := Precommit(repo, headAndStaged(frontend, &headDirs, staged, head, SuiteResult{}))
	if !res.Blocked || !strings.Contains(res.Message, "src/b.ts(3,1): error TS2304: new") {
		t.Fatalf("want a refusal naming the new line, got %+v", res)
	}
	if strings.Contains(res.Message, "error TS2322: old") {
		t.Fatalf("the refusal quotes a line HEAD already printed:\n%s", res.Message)
	}
}

// A run that adds many lines is quoted to twenty of them, with a count of
// the rest when there is a rest.
func TestDeclaredBaseline_TheRefusalQuotesTwentyNewLinesAndCountsTheRest(t *testing.T) {
	for _, n := range []int{20, 25} {
		repo, frontend := makeFrontendRepo(t, declaredTscLines)
		var many strings.Builder
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&many, "new error %02d\n", i)
		}
		var headDirs []string
		staged := func(string) string { return many.String() }
		head := func(string) string { return "" }
		res := Precommit(repo, headAndStaged(frontend, &headDirs, staged, head, SuiteResult{}))
		if !res.Blocked || !strings.Contains(res.Message, "new error 20") || strings.Contains(res.Message, "new error 21") {
			t.Fatalf("%d new lines: want lines 01-20 quoted and 21 not, got %+v", n, res)
		}
		if got, want := strings.Contains(res.Message, "more"), n > 20; got != want {
			t.Fatalf("%d new lines: counts a rest = %v, want %v:\n%s", n, got, want, res.Message)
		}
		if n > 20 && !strings.Contains(res.Message, fmt.Sprintf("and %d more", n-20)) {
			t.Fatalf("%d new lines: the refusal miscounts the lines it left out:\n%s", n, res.Message)
		}
	}
}

// A lines baseline only answers a failure: a pass runs nothing at HEAD, and
// a staged run that did not finish is refused as a timeout, not compared.
func TestDeclaredBaseline_APassOrATimeoutNeverRunsHead(t *testing.T) {
	for _, staged := range []SuiteResult{{Passed: true, Output: "fine\n"}, {TimedOut: true}} {
		repo, frontend := makeFrontendRepo(t, declaredTscLines)
		headRuns := 0
		run := func(r Runner, dir string) SuiteResult {
			switch {
			case !strings.Contains(cmdLine(r), "tsc"):
				return SuiteResult{Passed: true}
			case dir == frontend:
				return staged
			}
			headRuns++
			return SuiteResult{Output: "fine\n"}
		}
		res := Precommit(repo, run)
		if res.Blocked != staged.TimedOut || headRuns != 0 {
			t.Errorf("staged %+v: blocked=%v after %d HEAD run(s), want blocked=%v and none", staged, res.Blocked, headRuns, staged.TimedOut)
		}
	}
}

// A HEAD run that does not finish measured nothing: the commit is refused
// and says why, never waved through.
func TestDeclaredBaseline_AHeadRunThatTimesOutRefuses(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredTscLines)

	var headDirs []string
	same := func(c string) string { return oldError(c) + "\n" }
	res := Precommit(repo, headAndStaged(frontend, &headDirs, same, same, SuiteResult{TimedOut: true}))
	if !res.Blocked || !strings.Contains(res.Message, "no baseline at HEAD") {
		t.Fatalf("want a refusal naming the missing baseline, got %+v", res)
	}
}

// With no HEAD commit there is no tree to run in: the commit is refused and
// says why.
func TestDeclaredBaseline_NoHeadCommitRefuses(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	write(t, repo, ".gitignore", "node_modules/\n")
	write(t, repo, "aphrollo.toml", declaredTscLines)
	write(t, repo, "frontend/package.json", `{"name": "app"}`)
	write(t, repo, "frontend/src/a.ts", "export const a = 1\n")
	gitDo(t, repo, "add", ".")
	frontend := filepath.Join(repo, "frontend")

	var headDirs []string
	same := func(c string) string { return oldError(c) + "\n" }
	res := Precommit(repo, headAndStaged(frontend, &headDirs, same, same, SuiteResult{}))
	if !res.Blocked || !strings.Contains(res.Message, "no baseline at HEAD") || len(headDirs) != 0 {
		t.Fatalf("want a refusal naming the missing baseline and no HEAD run, got %+v (HEAD runs %v)", res, headDirs)
	}
}

// A root with no node_modules of its own gets its HEAD tree outside the
// repo, and is judged the same way.
func TestDeclaredBaseline_ARootWithoutNodeModulesRunsHeadOutsideIt(t *testing.T) {
	repo := makeTSRepo(t, map[string]string{
		"aphrollo.toml":         declaredTscLines,
		"frontend/package.json": `{"name": "app"}`,
		"frontend/src/a.ts":     "export const a = 1\n",
	})
	frontend := filepath.Join(repo, "frontend")
	write(t, repo, "frontend/src/b.ts", "export const b = 1\n")
	gitDo(t, repo, "add", ".")

	var headDirs []string
	same := func(c string) string { return oldError(c) + "\n" }
	if res := Precommit(repo, headAndStaged(frontend, &headDirs, same, same, SuiteResult{})); res.Blocked {
		t.Fatalf("a failure already at HEAD refused the commit:\n%s", res.Message)
	}
	if len(headDirs) != 1 {
		t.Fatalf("HEAD runs at %v, want one", headDirs)
	}
	if rel, err := filepath.Rel(repo, headDirs[0]); err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("HEAD ran at %s, want outside %s", headDirs[0], repo)
	}
}

// Each run's lines are read with its own checkout's path taken out and
// trailing whitespace trimmed, so the same error printed from two
// checkouts is one line. Nothing else is normalised.
func TestNewOutputLines_TheSameErrorAtTwoCheckoutsIsEqual(t *testing.T) {
	now, head := filepath.Join("/", "w", "lane"), filepath.Join("/", "tmp", "head-1")
	nowOut := oldError(now) + " \r\n" + "Error: Old\n"
	headOut := oldError(head) + "\n" + "error: old\n"
	got := newOutputLines(nowOut, now, headOut, head)
	if len(got) != 1 || got[0] != "Error: Old" {
		t.Fatalf("new lines = %q, want only the one whose case differs", got)
	}
}

// A command may be an inline table carrying a baseline; any other baseline
// or key is refused, not guessed at.
func TestDeclaredPrecommit_ReadsABaselineFromAnInlineTable(t *testing.T) {
	cmds, err := parseDeclaredCommands(`[{ argv = ["npx", "tsc"], baseline = "lines" }, ["eslint", "src"], {argv=["x"]}, { argv = ["y"], baseline = "none" }]`)
	if err != nil {
		t.Fatal(err)
	}
	want := []declaredCommand{
		{Argv: []string{"npx", "tsc"}, Baseline: "lines"},
		{Argv: []string{"eslint", "src"}},
		{Argv: []string{"x"}},
		{Argv: []string{"y"}, Baseline: "none"},
	}
	if fmt.Sprint(cmds) != fmt.Sprint(want) {
		t.Fatalf("commands = %v, want %v", cmds, want)
	}
	for _, bad := range []string{
		`[{ argv = ["tsc"], baseline = "exact" }]`,
		`[{ argv = ["tsc"], baselines = "lines" }]`,
		`[{ baseline = "lines" }]`,
	} {
		if _, err := parseDeclaredCommands(bad); err == nil {
			t.Errorf("%s: read without an error", bad)
		}
	}
}

// A root without a node_modules has nothing to link: HEAD's tree gets no
// node_modules of its own.
func TestDeclaredBaseline_ARootWithoutNodeModulesGetsNoLinkAtHead(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredTscLines)
	if err := os.RemoveAll(filepath.Join(frontend, "node_modules")); err != nil {
		t.Fatal(err)
	}
	var headDirs []string
	linked := map[string]bool{}
	staged := func(c string) string { return oldError(c) + "\n" }
	head := func(c string) string {
		_, err := os.Lstat(filepath.Join(c, "frontend", "node_modules"))
		linked[c] = err == nil
		return oldError(c) + "\n"
	}
	if res := Precommit(repo, headAndStaged(frontend, &headDirs, staged, head, SuiteResult{})); res.Blocked {
		t.Fatalf("a failure already at HEAD refused the commit:\n%s", res.Message)
	}
	if len(headDirs) != 1 || linked[filepath.Dir(headDirs[0])] {
		t.Fatalf("HEAD runs at %v; want one, with no node_modules in its root (linked: %v)", headDirs, linked)
	}
}
