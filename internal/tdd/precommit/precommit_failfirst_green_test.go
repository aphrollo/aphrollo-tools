package precommit

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// Issue #922, from borld's forge_jbeam: a commit changed the loader in
// src/latches.rs and added a test to tests/latches.rs. Fail-first proved the
// new test RED at HEAD and stopped there, so nothing ever ran it with the
// staged change applied. It was red there too, and the commit landed. A test
// that is red before the change and still red after it is the defect this
// gate exists to stop, so the proof also runs the same tests with the
// staged change and refuses the commit while they are red.

func TestPrecommit_FailFirst_RefusesARustTestStillRedWithTheChange(t *testing.T) {
	tddtest.RequireRealCargo(t)
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_jbeam\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "src/lib.rs", "pub mod latches;\n")
	write(t, root, "src/latches.rs", "pub fn substep() -> u32 {\n    1\n}\n")
	write(t, root, "tests/latches.rs", "#[test]\nfn loads() {\n    assert!(forge_jbeam::latches::substep() > 0);\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "latches")

	// The change moves substep to 2; the new test wants 3, so it is red at
	// HEAD and still red with the change.
	write(t, root, "src/latches.rs", "pub fn substep() -> u32 {\n    2\n}\n")
	write(t, root, "tests/latches.rs", "#[test]\nfn loads() {\n    assert!(forge_jbeam::latches::substep() > 0);\n}\n\n"+
		"#[test]\nfn a_loaded_latch_runs_the_carried_law() {\n    assert_eq!(forge_jbeam::latches::substep(), 3, \"not carried\");\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))

	if !res.Blocked {
		t.Fatal("the new test is red at HEAD and still red with the staged change; the commit must be refused")
	}
	if !strings.Contains(res.Message, "a_loaded_latch_runs_the_carried_law") {
		t.Fatalf("the refusal must name the test that is still red, got: %s", res.Message)
	}
}

func TestPrecommit_FailFirst_AdmitsARustTestTheChangeTurnsGreen(t *testing.T) {
	tddtest.RequireRealCargo(t)
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_jbeam\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "src/lib.rs", "pub mod latches;\n")
	write(t, root, "src/latches.rs", "pub fn substep() -> u32 {\n    1\n}\n")
	write(t, root, "tests/latches.rs", "#[test]\nfn loads() {\n    assert!(forge_jbeam::latches::substep() > 0);\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "latches")

	write(t, root, "src/latches.rs", "pub fn substep() -> u32 {\n    3\n}\n")
	write(t, root, "tests/latches.rs", "#[test]\nfn loads() {\n    assert!(forge_jbeam::latches::substep() > 0);\n}\n\n"+
		"#[test]\nfn a_loaded_latch_runs_the_carried_law() {\n    assert_eq!(forge_jbeam::latches::substep(), 3);\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))

	if res.Blocked {
		t.Fatalf("red at HEAD and green with the change is the proof the gate wants: %s", res.Message)
	}
}

// The Go proof runs the same function, so it shared the gap.
func TestPrecommit_FailFirst_RefusesAGoTestStillRedWithTheChange(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")

	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidgetIsThree(t *testing.T) {\n\tif Widget() != 3 {\n\t\tt.Fatal(Widget())\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, RunSuite(precommitTestTimeout)) })

	if !res.Blocked {
		t.Fatal("the new Go test is red at HEAD and still red with the staged change; the commit must be refused")
	}
	if !strings.Contains(stderr, "→ red-proven") || !strings.Contains(stderr, "→ still-red") {
		t.Fatalf("the stage must print the RED proof and then the still-red run, got:\n%s", stderr)
	}
	if !strings.Contains(res.Message, "TestWidgetIsThree") {
		t.Fatalf("the refusal must name the test that is still red, got: %s", res.Message)
	}
}

// redAtHeadThenGreen is a faked proof that behaves the way a correct commit
// does: each proof's first run (HEAD plus the staged tests) returns red, and
// its second (the staged change applied) returns green. Proofs alternate, so
// a caller running several proofs sees red, green, red, green.
func redAtHeadThenGreen(red SuiteResult, record func(Runner, string)) SuiteRunner {
	calls := 0
	return func(r Runner, dir string) SuiteResult {
		if record != nil {
			record(r, dir)
		}
		calls++
		if calls%2 == 0 {
			return SuiteResult{Passed: true, Output: "ok\n", Duration: red.Duration}
		}
		return red
	}
}
