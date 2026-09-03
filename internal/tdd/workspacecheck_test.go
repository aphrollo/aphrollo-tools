package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGate_ChecksTheWholeWorkspaceBeforeTheSuites pins the hole that let a
// non-compiling file reach main (borld, 2026-09-02: a lane added a struct
// field, forge_jbeam/tests/conformance.rs stopped compiling, and the gate ran
// only the TOUCHED crates' suites, so nobody found out until someone built
// the workspace by hand). A clippy `--tests` pass sits between clippy and
// fail-first: check-level, no codegen, and it covers every crate the change
// could have broken -- the touched ones and the clippy-clean crates
// downstream of them.
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
		if isCheckStage(cmd) && checkAt < 0 {
			checkAt = i
		}
		if strings.Contains(cmd, "nextest") || strings.HasPrefix(cmd, "test") {
			if suiteAt < 0 {
				suiteAt = i
			}
		}
	}
	if checkAt < 0 {
		t.Fatalf("no compile-coverage check ran; commands were %v", order)
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
		if isCheckStage(strings.Join(r.Args, " ")) {
			return SuiteResult{Passed: false, Output: diag + "\n  --> crates/forge_jbeam/tests/conformance.rs:12:9\n"}
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})
	if !res.Blocked {
		t.Fatal("a tree that does not compile must not be committed")
	}
	if !strings.Contains(res.Message, "E0560") {
		t.Fatalf("message = %q, want the first diagnostic", res.Message)
	}
}

// TestMechanical_AlsoRunsTheCompileCoverageCheck pins the merge half: a merge
// never fires pre-commit, and combining two lanes is exactly how a crate
// neither lane touched stops compiling.
func TestMechanical_AlsoRunsTheCompileCoverageCheck(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
	gitDo(t, root, "add", ".")

	checked := false
	Mechanical(root, func(r Runner, _ string) SuiteResult {
		if isCheckStage(strings.Join(r.Args, " ")) {
			checked = true
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})
	if !checked {
		t.Fatal("the merge gate must run the compile-coverage check too")
	}
}

// isCheckStage recognises the compile-coverage stage by the two lints that
// carry project laws. It used to be recognisable by `--workspace`; the stage
// is now scoped to the touched crates and their clippy-clean dependents, and
// the lints are what actually identify it.
func isCheckStage(args string) bool {
	return strings.HasPrefix(args, "clippy") && strings.Contains(args, "clippy::disallowed_methods")
}

// gateLogOf reads the gate.log written under the test's state dir.
func gateLogOf(t *testing.T, cfg string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	return string(data)
}

// TestWorkspaceStage_IsClippyWithTheTwoDeniedLints pins the amended stage:
// clippy subsumes check (a compile error still fails it), and denying exactly
// clippy::disallowed_methods and clippy::disallowed_types across the scope is
// what makes borld's clippy.toml laws enforceable beyond the clean crates. Every other
// lint stays at its default level here — the per-crate clippy-clean stage is
// where -D warnings applies.
func TestWorkspaceStage_IsClippyWithTheTwoDeniedLints(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
	gitDo(t, root, "add", ".")

	var checkArgs string
	Precommit(root, func(r Runner, _ string) SuiteResult {
		if args := strings.Join(r.Args, " "); isCheckStage(args) {
			checkArgs = args
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})

	for _, want := range []string{
		"--tests",
		"-D clippy::disallowed_methods",
		"-D clippy::disallowed_types",
	} {
		if !strings.Contains(checkArgs, want) {
			t.Fatalf("check stage = %q, want it to contain %q", checkArgs, want)
		}
	}
	if strings.Contains(checkArgs, "-D warnings") {
		t.Fatalf("check stage = %q — -D warnings belongs to the per-crate clippy-clean stage, not to every crate in the tree", checkArgs)
	}
}

// TestWorkspaceStage_NamesWhichKindOfFailure pins the two rejection kinds: a
// reader scanning gate.log must be able to tell "the tree does not compile"
// from "someone used a banned API", because they are different problems with
// different fixes.
func TestWorkspaceStage_NamesWhichKindOfFailure(t *testing.T) {
	cases := []struct {
		name, output, want string
	}{
		{"compile error", "error[E0560]: struct `Tire` has no field named `slip`\n", "check-rejected"},
		{"could not compile", "error: could not compile `forge_jbeam` (test) due to 1 previous error\n", "check-rejected"},
		{"disallowed method", "error: use of a disallowed method `std::time::Instant::now`\n  = note: `-D clippy::disallowed-methods` implied by the command line\n", "lint-rejected"},
		{"disallowed type", "error: use of a disallowed type `std::collections::HashMap`\n  = note: `clippy::disallowed_types` denied\n", "lint-rejected"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			root := makeCargoRepo(t)
			write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
			gitDo(t, root, "add", ".")

			res := Precommit(root, func(r Runner, _ string) SuiteResult {
				if isCheckStage(strings.Join(r.Args, " ")) {
					return SuiteResult{Passed: false, Output: c.output}
				}
				return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
			})
			if !res.Blocked {
				t.Fatal("the commit must be refused")
			}
			if log := gateLogOf(t, cfg); !strings.Contains(log, c.want) {
				t.Fatalf("gate.log has no %s entry:\n%s", c.want, log)
			}
		})
	}
}
