package precommit

import (
	"strings"
	"testing"
)

// loopSeed is a committed function that grows an argument list one element at
// a time in a loop and builds a Runner from it: the shape issue #996 found the
// exec call-site guard blind to.
const loopSeed = "package suite\n\nfunc narrowPytest(files []string) Runner {\n\targs := []string{\"-q\"}\n\tfor _, f := range files {\n\t\targs = append(args, f)\n\t}\n\treturn Runner{Cmd: \"pytest\", Args: args}\n}\n"

// loopRepo is a relatedRepo that also carries the loop-built-argument guard
// and one non-test file holding loopSeed.
func loopRepo(t *testing.T) string {
	t.Helper()
	root := relatedRepo(t)
	write(t, root, "internal/argvbatch/loopsites_test.go", "package argvbatch\n")
	write(t, root, "internal/tdd/suite/pytest.go", loopSeed)
	write(t, root, "internal/tdd/suite/plain.go", "package suite\n\nvar X = 1\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "seed loop")
	return root
}

// loopGuardArgs is the one command the stage runs for the loop-built table: its
// test, by name, in the argvbatch package.
const loopGuardArgs = "test -count=1 ./internal/argvbatch -run ^TestLoopBuiltArgSites_EveryArgumentListGrownInALoopIsBounded$"

// Issue #1079 (the call-site class): a commit that adds a function growing an
// argument list in a loop and handing it to a Runner holds no exec call and
// names no verb, so no guard ran, and the missing loopBuiltArgSites row failed
// only in CI.
func TestCallsiteGuardStage_RunsTheLoopBuiltGuardWhenAnArgumentListGrowsInALoop(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"a new function that grows the list in a loop (the #996 shape)", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/new.go", loopSeed)
		}},
		{"a new loop in a function that already builds a Runner", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/pytest.go", strings.Replace(loopSeed, "\treturn Runner", "\tfor _, g := range files {\n\t\targs = append(args, g)\n\t}\n\treturn Runner", 1))
		}},
		{"a changed Runner literal", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/pytest.go", strings.Replace(loopSeed, "Args: args}", "Args: args, Dir: \".\"}", 1))
		}},
		{"a Runner literal outside the gate's tree", func(t *testing.T, root string) {
			write(t, root, "internal/cli/new.go", "package cli\n\nvar r = Runner{Cmd: \"x\"}\n")
		}},
		{"a deleted loop", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/pytest.go", "package suite\n\nfunc narrowPytest(files []string) Runner {\n\treturn Runner{Cmd: \"pytest\"}\n}\n")
		}},
		{"a deleted file holding the loop", func(t *testing.T, root string) {
			gitDo(t, root, "rm", "-q", "internal/tdd/suite/pytest.go")
		}},
		{"a file the exec guard skips as generated", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/export.go", loopSeed)
		}},
		{"a tddtest directory outside the one the guard skips", func(t *testing.T, root string) {
			write(t, root, "internal/cli/tddtest/new.go", loopSeed)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loopRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{Passed: true}}
			if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked {
				t.Fatalf("a green guard blocked: %s", res.Message)
			}
			var got []string
			for _, r := range rec.runs {
				got = append(got, strings.Join(r.Args, " "))
			}
			if len(got) != 1 || got[0] != loopGuardArgs {
				t.Fatalf("runs = %q, want [%q]", got, loopGuardArgs)
			}
		})
	}
}

func TestCallsiteGuardStage_SkipsWhatCannotMoveALoopBuiltRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T, root string)
	}{
		{"an edit with no append and no Runner", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/plain.go", "package suite\n\nvar X = 2\n")
		}},
		{"a loop growing a list the guard does not name", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/new.go", "package suite\n\nfunc names(files []string) []string {\n\tvar out []string\n\tfor _, f := range files {\n\t\tout = append(out, f)\n\t}\n\treturn out\n}\n")
		}},
		{"a loop in a test file", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/suite/new_test.go", loopSeed)
		}},
		{"a loop under the tddtest directory the guard skips", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/internal/tddtest/new.go", loopSeed)
		}},
		{"a loop under a gotmp directory", func(t *testing.T, root string) {
			write(t, root, "internal/tdd/gotmp1/new.go", loopSeed)
		}},
		{"a loop in a non-Go file", func(t *testing.T, root string) {
			write(t, root, "README.md", "args = append(args, f)\nRunner{}\n")
		}},
		{"nothing staged", func(t *testing.T, root string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loopRepo(t)
			tc.stage(t, root)
			gitDo(t, root, "add", "-A")
			rec := &recordedRun{result: SuiteResult{}}
			if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
				t.Fatalf("blocked=%v runs=%v, want no run and no block", res.Blocked, rec.runs)
			}
		})
	}
}

func TestCallsiteGuardStage_SkipsARepoWithoutTheLoopBuiltTable(t *testing.T) {
	t.Parallel()
	root := relatedRepo(t)
	write(t, root, "internal/tdd/suite/new.go", loopSeed)
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{}
	if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked || len(rec.runs) != 0 {
		t.Fatalf("blocked=%v runs=%v, want no run in a repo with no loop-built table", res.Blocked, rec.runs)
	}
}

// One function that makes an exec call, names a verb and grows a list in a
// loop answers to all three tables, in the order the stage lists them.
func TestCallsiteGuardStage_RunsAllThreeGuardsOneChangeMoves(t *testing.T) {
	t.Parallel()
	root := loopRepo(t)
	write(t, root, "internal/tdd/suite/new.go", "package suite\n\nfunc a(files []string) bool {\n\tvar args []string\n\tfor _, f := range files {\n\t\targs = append(args, f)\n\t}\n\texec.Command(\"git\", args...)\n\treturn files[1] == \"related\"\n}\n")
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Passed: true}}
	if res := callsiteGuardStage("precommit", root, rec.run); res.Blocked {
		t.Fatalf("a green guard blocked: %s", res.Message)
	}
	var got []string
	for _, r := range rec.runs {
		got = append(got, strings.Join(r.Args, " "))
	}
	want := []string{"test -count=1 ./internal/argvbatch -run ^TestExecCallSites_EverySpreadArgumentListIsBatchedOrBounded$", relatedGuardArgs, loopGuardArgs}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("runs = %q, want %q", got, want)
	}
}

func TestCallsiteGuardStage_BlocksOnAFailingLoopBuiltGuard(t *testing.T) {
	t.Parallel()
	root := loopRepo(t)
	write(t, root, "internal/tdd/suite/new.go", loopSeed)
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Output: "internal/tdd/suite/new.go:narrowPytest grows an argument list in a loop and is not accounted for\n"}}
	res := callsiteGuardStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/tdd/suite/new.go:narrowPytest") {
		t.Fatalf("result = %+v, want a block quoting the unaccounted builder", res)
	}
}

// go test exits 0 when -run matches nothing: a renamed loop-built test must not
// turn the stage into a pass.
func TestCallsiteGuardStage_BlocksWhenTheLoopBuiltGuardNoLongerExists(t *testing.T) {
	t.Parallel()
	root := loopRepo(t)
	write(t, root, "internal/tdd/suite/new.go", loopSeed)
	gitDo(t, root, "add", "-A")
	rec := &recordedRun{result: SuiteResult{Passed: true, Output: "testing: warning: no tests to run\nok\n"}}
	res := callsiteGuardStage("precommit", root, rec.run)
	if !res.Blocked || !strings.Contains(res.Message, "TestLoopBuiltArgSites_EveryArgumentListGrownInALoopIsBounded") {
		t.Fatalf("result = %+v, want a block naming the loop-built test that ran nothing", res)
	}
}

// The loop-built guard is wired into the commit gate through the stage.
func TestPrecommitDecide_RefusesANewLoopBuiltListTheTableDoesNotList(t *testing.T) {
	t.Parallel()
	root := loopRepo(t)
	write(t, root, "internal/tdd/suite/new.go", loopSeed)
	gitDo(t, root, "add", "-A")
	run := func(r Runner, _ string) SuiteResult {
		if strings.Contains(strings.Join(r.Args, " "), "TestLoopBuiltArgSites") {
			return SuiteResult{Output: "internal/tdd/suite/new.go:narrowPytest is not accounted for\n"}
		}
		return SuiteResult{Passed: true}
	}
	res := precommitDecide(root, run)
	if !res.Blocked || !strings.Contains(res.Message, "internal/tdd/suite/new.go:narrowPytest") {
		t.Fatalf("precommitDecide = %+v, want the loop-built guard's block", res)
	}
}

// loopSpanSource holds one function that grows an argument list in a loop and
// builds a Runner at lines 5-11, between two functions that do neither:
//
//	3   func before() {}
//	4
//	5   func target(files []string) Runner {
//	6   	args := []string{"-q"}
//	7   	for _, f := range files {
//	8   		args = append(args, f)
//	9   	}
//	10  	return Runner{Cmd: "pytest", Args: args}
//	11  }
//	12
//	13  func after() {}
const loopSpanSource = "package suite\n\nfunc before() {}\n\nfunc target(files []string) Runner {\n\targs := []string{\"-q\"}\n\tfor _, f := range files {\n\t\targs = append(args, f)\n\t}\n\treturn Runner{Cmd: \"pytest\", Args: args}\n}\n\nfunc after() {}\n"

func loopSpanRepo(t *testing.T) string {
	t.Helper()
	root := loopRepo(t)
	write(t, root, "internal/tdd/suite/span.go", loopSpanSource)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "loop span")
	return root
}

// The loop-built guard runs when a commit edits a function that grows a list in
// a loop and builds a Runner, even when no changed line is the append or the
// literal: the table is keyed by function.
func TestLoopGuardNeeded_JudgesTheFunctionARangeEditsByItsExactBounds(t *testing.T) {
	t.Parallel()
	edit := func(old, repl string) string { return strings.Replace(loopSpanSource, old, repl, 1) }
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"the line before the function", edit("}\n\nfunc target", "}\n// note\nfunc target"), false},
		{"the function's first line", edit("func target(files []string) Runner {", "func target(files []string) Runner { // edited"), true},
		{"a line inside the function that is neither the append nor the literal", edit("[]string{\"-q\"}", "[]string{\"-q\", \"-x\"}"), true},
		{"the function's last line", edit("Args: args}\n}", "Args: args}\n} // edited"), true},
		{"the line after the function", edit("}\n\nfunc after", "}\n// note\nfunc after"), false},
		{"a function with no loop and no Runner", edit("func before() {}", "func before() { _ = 1 }"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loopSpanRepo(t)
			write(t, root, "internal/tdd/suite/span.go", tc.src)
			gitDo(t, root, "add", "-A")
			if got := loopGuard.needed(root); got != tc.want {
				t.Fatalf("loopGuard.needed = %v, want %v", got, tc.want)
			}
		})
	}
}

// A function that only grows a list, or only builds a Runner, is not one the
// guard lists: an edit to it that touches neither line moves no row.
func TestLoopGuardNeeded_NeedsBothTheAppendAndTheSpawnInOneFunction(t *testing.T) {
	t.Parallel()
	const appendOnly = "package suite\n\nfunc collect(files []string) []string {\n\tvar args []string\n\tfor _, f := range files {\n\t\targs = append(args, f)\n\t}\n\treturn args\n}\n"
	const runnerOnly = "package suite\n\nfunc one() Runner {\n\tx := 1\n\t_ = x\n\treturn Runner{Cmd: \"go\"}\n}\n"
	const execOnly = "package suite\n\nfunc spawn(p []string) {\n\tx := 1\n\t_ = x\n\texec.Command(\"git\", p...)\n}\n"
	const execAndAppend = "package suite\n\nfunc both(files []string) {\n\tx := 1\n\t_ = x\n\tvar args []string\n\tfor _, f := range files {\n\t\targs = append(args, f)\n\t}\n\texec.Command(\"git\", args...)\n}\n"
	for _, tc := range []struct {
		name         string
		src, old, to string
		want         bool
	}{
		{"a loop that starts nothing", appendOnly, "var args []string", "var args = []string(nil)", false},
		{"a Runner built with no loop", runnerOnly, "x := 1", "x := 2", false},
		{"an exec call with no loop", execOnly, "x := 1", "x := 2", false},
		{"a loop that hands its list to an exec call", execAndAppend, "x := 1", "x := 2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loopRepo(t)
			write(t, root, "internal/tdd/suite/pair.go", tc.src)
			gitDo(t, root, "add", ".")
			gitDo(t, root, "commit", "-q", "-m", "pair")
			write(t, root, "internal/tdd/suite/pair.go", strings.Replace(tc.src, tc.old, tc.to, 1))
			gitDo(t, root, "add", "-A")
			if got := loopGuard.needed(root); got != tc.want {
				t.Fatalf("loopGuard.needed = %v, want %v", got, tc.want)
			}
		})
	}
}

// A commit that moves the loop into a new function changes the enclosing
// function's lines and leaves the append and the literal as context.
func TestLoopGuardNeeded_SeesALoopMovedIntoANewFunction(t *testing.T) {
	t.Parallel()
	root := loopRepo(t)
	write(t, root, "internal/tdd/suite/move.go", "package suite\n\nfunc a(files []string) Runner {\n\tx := 1\n\t_ = x\n\targs := []string{}\n\tfor _, f := range files {\n\t\targs = append(args, f)\n\t}\n\treturn Runner{Args: args}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "move base")
	write(t, root, "internal/tdd/suite/move.go", "package suite\n\nfunc a(files []string) Runner {\n\tx := 1\n\t_ = x\n\treturn b(files)\n}\n\nfunc b(files []string) Runner {\n\targs := []string{}\n\tfor _, f := range files {\n\t\targs = append(args, f)\n\t}\n\treturn Runner{Args: args}\n}\n")
	gitDo(t, root, "add", "-A")
	diff, err := git(root, "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--", "*.go")
	if err != nil {
		t.Fatal(err)
	}
	if loopGuard.diffChanges(diff) {
		t.Fatalf("the diff changes the append or the literal itself, so this is not the moved-loop case:\n%s", diff)
	}
	if !loopGuard.needed(root) {
		t.Fatalf("loopGuard.needed = false for a loop moved into a new function:\n%s", diff)
	}
}

// A staged file the loop-built table already names has a row that a change to
// it can invalidate, whatever the changed lines say.
func TestLoopGuardNeeded_SeesAStagedFileTheTableNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		table string
		want  bool
	}{
		{"a row for the file", "package argvbatch\n\nvar t = map[string]string{\n\t\"internal/tdd/suite/plain.go:run\": \"bounded\",\n}\n", true},
		{"a row for another file", "package argvbatch\n\nvar t = map[string]string{\n\t\"internal/tdd/suite/other.go:run\": \"bounded\",\n}\n", false},
		{"a row for a longer path", "package argvbatch\n\nvar t = map[string]string{\n\t\"internal/tdd/suite/plain.gox:run\": \"bounded\",\n}\n", false},
		{"a row for a deeper path", "package argvbatch\n\nvar t = map[string]string{\n\t\"x/internal/tdd/suite/plain.go:run\": \"bounded\",\n}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := makeGoRepo(t)
			write(t, root, "internal/argvbatch/loopsites_test.go", tc.table)
			write(t, root, "internal/tdd/suite/plain.go", "package suite\n\nvar X = 1\n")
			gitDo(t, root, "add", ".")
			gitDo(t, root, "commit", "-q", "-m", "seed")
			write(t, root, "internal/tdd/suite/plain.go", "package suite\n\nvar X = 2\n")
			gitDo(t, root, "add", "-A")
			if got := loopGuard.needed(root); got != tc.want {
				t.Fatalf("loopGuard.needed = %v, want %v", got, tc.want)
			}
		})
	}
}

// A staged file that does not parse cannot be shown to hold no loop.
func TestLoopGuardNeeded_RunsForAStagedFileItCannotParse(t *testing.T) {
	t.Parallel()
	root := loopRepo(t)
	write(t, root, "internal/tdd/suite/plain.go", "package suite\n\nvar X = = 2\n")
	gitDo(t, root, "add", "-A")
	if !loopGuard.needed(root) {
		t.Fatal("loopGuard.needed = false for a staged Go file that does not parse")
	}
}

// The loop-built guard answers to its own trigger: a growing list or a Runner
// literal runs it alone, and an exec call or a verb with neither leaves it be.
func TestSiteGuards_TheLoopGuardRunsOnlyForALoopOrARunner(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		file, src string
		loop      bool
	}{
		{"a Runner literal", "internal/cli/new.go", "package cli\n\nvar r = Runner{Cmd: \"x\"}\n", true},
		{"an append to a list named args", "internal/cli/new.go", "package cli\n\nfunc a(f string) { var cmdArgs []string; cmdArgs = append(cmdArgs, f) }\n", true},
		{"an exec call with a spread list", "internal/cli/new.go", "package cli\n\nfunc a(p []string) { exec.Command(\"git\", p...) }\n", false},
		{"a verb", "internal/tdd/postedit/new.go", "package postedit\n\nfunc a(v string) bool { return v == \"related\" }\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loopRepo(t)
			write(t, root, tc.file, tc.src)
			gitDo(t, root, "add", "-A")
			if got := loopGuard.needed(root); got != tc.loop {
				t.Errorf("loopGuard.needed = %v, want %v", got, tc.loop)
			}
		})
	}
}
