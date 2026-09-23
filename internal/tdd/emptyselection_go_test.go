package tdd

import (
	"errors"
	"strings"
	"testing"
)

// The Go sibling of issue #730. A Go source edit narrows to its own package
// (`go test ./internal/proc`), and a package with no test file of its own
// selects nothing there: its tests live in the packages that import it. That
// run used to read as writing-test — "scaffolding, no tests yet" — for code
// the importers' tests exercise every day, and nothing ran them. It climbs
// the same ladder a cargo run does: one rung, to the packages whose tests
// reach the edited one.

const goNoTestFilesOutput = "?   \texample.com/m/internal/proc\t[no test files]\n"

const goImporterPassedOutput = "=== RUN   TestSpawnReapsTheChild\n" +
	"--- PASS: TestSpawnReapsTheChild (0.00s)\n" +
	"PASS\n" +
	"ok  \texample.com/m/internal/tdd\t0.012s\n"

// mkGoModule writes a module with a test-less package, and returns its root.
func mkGoModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n")
	write(t, root, "internal/proc/proc.go", "package proc\n\nfunc Spawn() int { return 1 }\n")
	write(t, root, "internal/tdd/tdd.go", "package tdd\n\nimport \"example.com/m/internal/proc\"\n\nfunc Run() int { return proc.Spawn() }\n")
	return root
}

// TestGoWideningSteps_ClimbToTheImportersWhoseTestsReachThePackage pins the
// Go ladder: one rung, the reaching packages minus the ones that already
// selected nothing, and no rung at all where there is nothing to add, where
// the reach cannot be read, or where the run is a test file's own tree (a
// test file that selects nothing is scaffolding, and says so).
func TestGoWideningSteps_ClimbToTheImportersWhoseTestsReachThePackage(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		reach map[string][]string
		err   error
		want  string
	}{
		{"importers reach it", []string{"test", "./internal/proc"},
			map[string][]string{"internal/proc": {"cmd/aphrollo", "internal/proc", "internal/tdd"}}, nil,
			"test ./cmd/aphrollo ./internal/tdd"},
		{"nothing else reaches it", []string{"test", "./internal/proc"},
			map[string][]string{"internal/proc": {"internal/proc"}}, nil, ""},
		{"the reach cannot be read", []string{"test", "./internal/proc"}, nil, errors.New("go list failed"), ""},
		{"a test file's own tree", []string{"test", "./internal/proc/..."},
			map[string][]string{"internal/proc": {"internal/proc", "internal/tdd"}}, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer stubGoTestReach(t, func(_, dir string) ([]string, error) { return c.reach[dir], c.err })()
			steps := goWideningSteps(Runner{Cmd: "go", Args: c.args}, "/ws")
			got := ""
			if len(steps) == 1 {
				got = strings.Join(steps[0].Args, " ")
			} else if len(steps) > 1 {
				t.Fatalf("a Go ladder has at most one rung, got %d", len(steps))
			}
			if got != c.want {
				t.Fatalf("rung for %v = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// TestPostEdit_DeferredGoEditInATestlessPackage_RunsTheImportersTests drives
// the path the real hook takes: the narrowed package selects nothing, the
// importers' tests run inside the same budget, and their verdict is the line.
func TestPostEdit_DeferredGoEditInATestlessPackage_RunsTheImportersTests(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkGoModule(t)
	defer stubGoTestReach(t, func(_, dir string) ([]string, error) {
		return []string{"internal/proc", "internal/tdd"}, nil
	})()
	spawned := scriptedPhases(t, map[string]scriptedPhase{
		"go test ./internal/proc": {out: &PhaseOutcome{ExitCode: 0}, log: goNoTestFilesOutput},
		"go test ./internal/tdd":  {out: &PhaseOutcome{ExitCode: 0}, log: goImporterPassedOutput},
	})

	got := PostEdit(postPayload("Edit", root+"/internal/proc/proc.go"), fakeRun(true, "the foreground runner must not be used"))

	if strings.Join(*spawned, "|") != "go test ./internal/proc|go test ./internal/tdd" {
		t.Fatalf("want the narrowed package then the importer rung, spawned %v", *spawned)
	}
	if strings.Contains(got, string(WritingTest)) {
		t.Fatalf("a source edit whose importers' tests ran is not scaffolding, got: %s", got)
	}
	if !strings.Contains(got, "go test ./internal/tdd in ") || !strings.Contains(got, "green") {
		t.Fatalf("want the importer rung's green naming its command, got: %s", got)
	}
}

// TestPostEdit_GoTestFileWithNoTestYet_StaysWritingTest pins the edge of the
// Go ladder: a TEST edit narrows to its package's tree, and a file that
// declares no test yet selecting nothing is scaffolding, not a missed
// filter. It must neither climb to the importers nor be called untested.
func TestPostEdit_GoTestFileWithNoTestYet_StaysWritingTest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkGoModule(t)
	write(t, root, "internal/proc/proc_test.go", "package proc\n")
	defer stubGoTestReach(t, func(_, dir string) ([]string, error) {
		return []string{"internal/proc", "internal/tdd"}, nil
	})()
	var seen []string
	got := PostEdit(postPayload("Edit", root+"/internal/proc/proc_test.go"), scriptedRunner(t, &seen, map[string]SuiteResult{
		"go test ./internal/proc/...": {Passed: true, Output: "ok  \texample.com/m/internal/proc\t0.002s [no tests to run]\n"},
	}))

	if len(seen) != 1 {
		t.Fatalf("a test file with no test yet must not climb to the importers, ran %v", seen)
	}
	if strings.Contains(got, strings.ToUpper(NoTestsSelected)) {
		t.Fatalf("a test file with no test yet is scaffolding, not an empty selection to report, got: %s", got)
	}
}

// TestPostEdit_GoEditNothingReaches_IsInconclusiveNotScaffolding pins the top
// of the Go ladder: when no other package's tests reach the edited one, the
// run tested nothing and says so in the inconclusive family's words.
func TestPostEdit_GoEditNothingReaches_IsInconclusiveNotScaffolding(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkGoModule(t)
	defer stubGoTestReach(t, func(_, dir string) ([]string, error) { return []string{dir}, nil })()
	var seen []string
	got := PostEdit(postPayload("Edit", root+"/internal/proc/proc.go"), scriptedRunner(t, &seen, map[string]SuiteResult{
		"go test ./internal/proc": {Passed: true, Output: goNoTestFilesOutput},
	}))

	if strings.Contains(got, "green") || strings.Contains(got, string(WritingTest)) {
		t.Fatalf("a package no test reaches was not tested, got: %s", got)
	}
	if !strings.Contains(got, strings.ToUpper(NoTestsSelected)) || !strings.Contains(got, "NOT tested") {
		t.Fatalf("want the inconclusive NO-TESTS-SELECTED line, got: %s", got)
	}
}
