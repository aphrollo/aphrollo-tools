package tdd

import (
	"strings"
	"testing"
)

// TestGate_ChecksTheWholeWorkspaceBeforeTheSuites pins the hole that let a
// non-compiling file reach main (borld, 2026-09-02: a lane added a struct
// field, forge_jbeam/tests/conformance.rs stopped compiling, and the gate ran
// only the TOUCHED crates' suites, so nobody found out until someone built
// the workspace by hand). A whole-workspace `cargo check --tests` sits
// between clippy and fail-first: check-level, no codegen, and it sees every
// crate the change could have broken.
func TestGate_ChecksTheWholeWorkspaceBeforeTheSuites(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
	gitDo(t, root, "add", ".")

	var order []string
	run := func(r Runner, _ string) SuiteResult {
		order = append(order, strings.Join(r.Args, " "))
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	}
	if res := Precommit(root, run); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	checkAt, suiteAt := -1, -1
	for i, cmd := range order {
		if strings.Contains(cmd, "check --workspace --tests") && checkAt < 0 {
			checkAt = i
		}
		if strings.Contains(cmd, "nextest") || strings.HasPrefix(cmd, "test") {
			if suiteAt < 0 {
				suiteAt = i
			}
		}
	}
	if checkAt < 0 {
		t.Fatalf("no whole-workspace check ran; commands were %v", order)
	}
	if suiteAt >= 0 && checkAt > suiteAt {
		t.Fatalf("the check ran AFTER the suites (%v) — the cheapest stage that can catch this must come first", order)
	}
}

// TestGate_WorkspaceCheckRejectsAndNamesTheDiagnostic pins the rejection: a
// commit that does not compile must not land, and the message has to carry
// the first diagnostic or the author has to reproduce the build to find out
// what broke.
func TestGate_WorkspaceCheckRejectsAndNamesTheDiagnostic(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
	gitDo(t, root, "add", ".")

	const diag = "error[E0560]: struct `Tire` has no field named `slip`"
	res := Precommit(root, func(r Runner, _ string) SuiteResult {
		if strings.Contains(strings.Join(r.Args, " "), "check --workspace") {
			return SuiteResult{Passed: false, Output: diag + "\n  --> crates/forge_jbeam/tests/conformance.rs:12:9\n"}
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})
	if !res.Blocked {
		t.Fatal("a workspace that does not compile must not be committed")
	}
	if !strings.Contains(res.Message, "E0560") {
		t.Fatalf("message = %q, want the first diagnostic", res.Message)
	}
}

// TestMechanical_AlsoChecksTheWholeWorkspace pins the merge half: a merge
// never fires pre-commit, and combining two lanes is exactly how a crate
// neither lane touched stops compiling.
func TestMechanical_AlsoChecksTheWholeWorkspace(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
	gitDo(t, root, "add", ".")

	checked := false
	Mechanical(root, func(r Runner, _ string) SuiteResult {
		if strings.Contains(strings.Join(r.Args, " "), "check --workspace --tests") {
			checked = true
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})
	if !checked {
		t.Fatal("the merge gate must check the whole workspace too")
	}
}
