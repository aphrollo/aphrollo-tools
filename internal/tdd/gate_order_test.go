package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// orderedGateRepo is a cargo repo declaring BOTH aphrollo metadata lists,
// with a staged source change — the shape every ordering assertion below
// needs.
func orderedGateRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"ratchet\"]\nclippy-clean = [\"m\"]\n")
	write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")
	return root
}

// stageOf names which gate stage a runner belongs to, for order assertions.
func stageOf(r Runner) string {
	args := strings.Join(r.Args, " ")
	switch {
	case strings.HasPrefix(args, "fmt"):
		return "fmt"
	case strings.HasPrefix(args, "clippy") && strings.Contains(args, "clippy::disallowed_methods"):
		// The compile-coverage stage: clippy subsumes check, and it denies the
		// two lints that carry project laws. Named by those lints rather than
		// by its crate selection, which is now scoped like every other stage.
		return "check"
	case strings.HasPrefix(args, "clippy"):
		return "clippy"
	case strings.Contains(args, "-p ratchet"):
		return "always-run"
	default:
		return "suite"
	}
}

// TestPrecommit_StagesRunCheapestFirst pins the gate's ORDER: milliseconds of
// rustfmt, then the pure guard crate, then clippy, then the touched crates'
// full test build+link+run. Before this the heaviest stage ran first, so a
// commit with a formatting slip paid twenty minutes to be told about a space.
func TestPrecommit_StagesRunCheapestFirst(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := orderedGateRepo(t)

	var order []string
	Precommit(root, func(r Runner, _ string) SuiteResult {
		order = append(order, stageOf(r))
		return SuiteResult{Passed: true}
	})

	// No "suite": the commit gate stops after the cheap stages. The touched
	// packages' tests run at the merge instead — see
	// TestPrecommit_RunsNoSuiteAtCommitTime for the measurement.
	want := []string{"fmt", "always-run", "clippy", "check"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("stage order = %v, want %v", order, want)
	}
}

// TestPrecommit_AlwaysRunIsItsOwnInvocation pins that the workspace guard
// crate is NOT bundled into the touched-crate command: `-p ratchet -p client`
// builds client before ratchet's cheap pure-crate suite can say anything, so
// the guard stops being the fast check it is.
func TestPrecommit_AlwaysRunIsItsOwnInvocation(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := orderedGateRepo(t)

	var suiteArgs []string
	Precommit(root, func(r Runner, _ string) SuiteResult {
		if stageOf(r) == "suite" {
			suiteArgs = append(suiteArgs, strings.Join(r.Args, " "))
		}
		return SuiteResult{Passed: true}
	})

	for _, args := range suiteArgs {
		if strings.Contains(args, "ratchet") {
			t.Fatalf("the always-run package must not ride along in the touched-crate run, got %q", args)
		}
		if !strings.Contains(args, "-p m") {
			t.Fatalf("the touched-crate run must still be scoped to m, got %q", args)
		}
	}
}

// TestPrecommit_StopsAtTheFirstFailingStage pins the point of ordering: a
// rejection from a cheap stage means no expensive stage runs at all.
func TestPrecommit_StopsAtTheFirstFailingStage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := orderedGateRepo(t)

	var ran []string
	res := Precommit(root, func(r Runner, _ string) SuiteResult {
		stage := stageOf(r)
		ran = append(ran, stage)
		if stage == "fmt" {
			return SuiteResult{Passed: false, Output: "Diff in src/widget.rs at line 1:\n"}
		}
		return SuiteResult{Passed: true}
	})
	if !res.Blocked {
		t.Fatal("a failing fmt stage must block")
	}
	if strings.Join(ran, ",") != "fmt" {
		t.Fatalf("stages run = %v, want fmt alone — nothing after the first failure", ran)
	}
}

// TestPrecommit_GateLogNamesTheRejectingStage pins the trail: the log line
// says WHICH stage rejected, so a session reading gate.log after a block
// knows whether it was formatting, a guard crate, a lint or the suite.
func TestPrecommit_GateLogNamesTheRejectingStage(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := orderedGateRepo(t)

	Precommit(root, func(r Runner, _ string) SuiteResult {
		if stageOf(r) == "always-run" {
			return SuiteResult{Passed: false, Output: "test result: FAILED. 1 failed"}
		}
		return SuiteResult{Passed: true}
	})

	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(data), "always-run-blocked") {
		t.Fatalf("gate.log must name the stage that rejected, got:\n%s", data)
	}
}

// TestPrecommit_AlwaysRunResultIsCached pins that the guard crate's green is
// remembered under the same content key as everything else: an identical
// tree at merge must not re-run it.
func TestPrecommit_AlwaysRunResultIsCached(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := orderedGateRepo(t)

	count := func() int {
		n := 0
		Precommit(root, func(r Runner, _ string) SuiteResult {
			if stageOf(r) == "always-run" {
				n++
			}
			return SuiteResult{Passed: true}
		})
		return n
	}
	if got := count(); got != 1 {
		t.Fatalf("first run must run the guard crate once, got %d", got)
	}
	if got := count(); got != 0 {
		t.Fatalf("an identical tree must reuse the guard crate's green, ran it %d more times", got)
	}
}

// TestMechanical_MergeGateSharesTheSameOrder pins that the merge gate is the
// same pipeline minus fail-first: a merge that breaks formatting or the
// guard crate is told so before it pays for the suites.
func TestMechanical_MergeGateSharesTheSameOrder(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := orderedGateRepo(t)

	var order []string
	Mechanical(root, func(r Runner, _ string) SuiteResult {
		order = append(order, stageOf(r))
		return SuiteResult{Passed: true}
	})
	want := []string{"fmt", "always-run", "clippy", "check", "suite"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("merge-gate stage order = %v, want %v", order, want)
	}
}
