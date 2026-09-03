package tdd

import (
	"strings"
	"testing"
)

// cargoQualityRepo is a one-crate cargo repo with a staged change, plus
// whatever `[workspace.metadata.aphrollo]` the test needs. The mechanical
// stage is what runs first, so every stub below answers it green and the
// test is only ever about what runs AFTER it.
func cargoQualityRepo(t *testing.T, metadata string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n"+metadata)
	write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")
	return root
}

// cargoArgsOf renders a runner's argv for matching in the stubs below.
func cargoArgsOf(r Runner) string { return strings.Join(r.Args, " ") }

// TestPrecommit_Quality_FmtCheckRunsForEveryTouchedCrate pins the format
// half: a commit that touched a cargo crate is format-checked for THAT
// crate — the check nobody runs by hand, which then lands as noise in the
// next person's diff.
func TestPrecommit_Quality_FmtCheckRunsForEveryTouchedCrate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := cargoQualityRepo(t, "")

	var seen []string
	res := Precommit(root, func(r Runner, _ string) SuiteResult {
		seen = append(seen, cargoArgsOf(r))
		return SuiteResult{Passed: true}
	})
	if res.Blocked {
		t.Fatalf("a clean crate must not block: %s", res.Message)
	}
	if !containsArgs(seen, "fmt --check -p m") {
		t.Fatalf("expected a `cargo fmt --check -p m` stage, ran: %v", seen)
	}
}

// TestPrecommit_Quality_FmtDiffBlocksAndNamesTheCrate pins the rejection:
// unformatted code fails the commit, and the message says which crate and
// what the tool actually reported — a block nobody can act on is a block
// that gets bypassed.
func TestPrecommit_Quality_FmtDiffBlocksAndNamesTheCrate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := cargoQualityRepo(t, "")

	res := Precommit(root, func(r Runner, _ string) SuiteResult {
		if strings.HasPrefix(cargoArgsOf(r), "fmt") {
			return SuiteResult{Passed: false, Output: "Diff in /repo/src/widget.rs at line 1:\n-pub fn widget()\n+pub fn  widget()\n"}
		}
		return SuiteResult{Passed: true}
	})
	if !res.Blocked {
		t.Fatal("an unformatted crate must block the commit")
	}
	if !strings.Contains(res.Message, "m") || !strings.Contains(res.Message, "Diff in") {
		t.Fatalf("the block must name the crate and the first diagnostic, got: %s", res.Message)
	}
}

// TestPrecommit_Quality_ClippyOnlyForDeclaredCrates pins the opt-in: a
// workspace that never declared `clippy-clean` is not lint-gated at all
// (most crates in a large tree carry warnings, and a commit gate that fails
// on them is one nobody can use), while a declared crate is.
func TestPrecommit_Quality_ClippyOnlyForDeclaredCrates(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	undeclared := cargoQualityRepo(t, "")
	var seen []string
	Precommit(undeclared, func(r Runner, _ string) SuiteResult {
		seen = append(seen, cargoArgsOf(r))
		return SuiteResult{Passed: true}
	})
	for _, args := range seen {
		// The compile-coverage stage always runs clippy (it subsumes check and
		// denies the two law-carrying lints); what this test is about is the
		// PER-CRATE -D warnings stage, which needs the declaration.
		if strings.HasPrefix(args, "clippy") && strings.Contains(args, "-D warnings") {
			t.Fatalf("a workspace with no clippy-clean list must not run per-crate clippy, ran: %v", seen)
		}
	}

	declared := cargoQualityRepo(t, "[workspace.metadata.aphrollo]\nclippy-clean = [\"m\"]\n")
	seen = nil
	Precommit(declared, func(r Runner, _ string) SuiteResult {
		seen = append(seen, cargoArgsOf(r))
		return SuiteResult{Passed: true}
	})
	if !containsArgs(seen, "clippy -p m --tests -- -D warnings") {
		t.Fatalf("a declared crate must be lint-gated with warnings denied, ran: %v", seen)
	}
}

// TestPrecommit_Quality_ClippyWarningBlocksAndNamesTheDiagnostic pins that
// a declared-clean crate that warns fails the commit with the diagnostic in
// hand: the whole value of "this crate is clean" is that the FIRST warning
// is the one being introduced now.
func TestPrecommit_Quality_ClippyWarningBlocksAndNamesTheDiagnostic(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := cargoQualityRepo(t, "[workspace.metadata.aphrollo]\nclippy-clean = [\"m\"]\n")

	res := Precommit(root, func(r Runner, _ string) SuiteResult {
		if strings.HasPrefix(cargoArgsOf(r), "clippy") {
			return SuiteResult{Passed: false, Output: "warning: unused variable: `x`\n  --> src/widget.rs:1:5\nerror: could not compile `m` due to 1 warning\n"}
		}
		return SuiteResult{Passed: true}
	})
	if !res.Blocked {
		t.Fatal("a warning in a crate declared clippy-clean must block the commit")
	}
	if !strings.Contains(res.Message, "unused variable") {
		t.Fatalf("the block must carry the first diagnostic, got: %s", res.Message)
	}
}

// TestPrecommit_Quality_SkippedEntirelyForNonCargoCommits pins the scope: a
// Go or Python commit must never pay for a cargo format/lint pass.
func TestPrecommit_Quality_SkippedEntirelyForNonCargoCommits(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []string
	Precommit(root, func(r Runner, _ string) SuiteResult {
		seen = append(seen, r.Cmd+" "+cargoArgsOf(r))
		return SuiteResult{Passed: true}
	})
	for _, cmd := range seen {
		if strings.Contains(cmd, "fmt --check") || strings.Contains(cmd, "clippy") {
			t.Fatalf("a non-cargo commit must run no cargo quality stage, ran: %v", seen)
		}
	}
}

// TestCargoAphrolloPackages_ReadsEitherKey pins the metadata reader both
// keys share: the workspace's own manifest declares which packages always
// run and which are lint-gated, and an absent key is simply an empty list.
func TestCargoAphrolloPackages_ReadsEitherKey(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "Cargo.toml",
		"[workspace]\n[workspace.metadata.aphrollo]\nalways-run = [\"ratchet\"]\nclippy-clean = [\"server\", \"shared\"]\n")

	if got := cargoAphrolloPackages(ws, "clippy-clean"); strings.Join(got, ",") != "server,shared" {
		t.Errorf("clippy-clean = %v, want [server shared]", got)
	}
	if got := cargoAphrolloPackages(ws, "always-run"); strings.Join(got, ",") != "ratchet" {
		t.Errorf("always-run = %v, want [ratchet]", got)
	}
	if got := cargoAphrolloPackages(ws, "no-such-key"); len(got) != 0 {
		t.Errorf("an absent key must read as empty, got %v", got)
	}
}

func containsArgs(seen []string, want string) bool {
	for _, s := range seen {
		if s == want {
			return true
		}
	}
	return false
}

// isQualityRunner lets the SCOPING tests keep asserting on the suite
// command alone: the quality stage adds a `cargo fmt --check` (and
// sometimes a clippy) run per touched crate, which those tests are not
// about and which has its own coverage above.
// isQualityRunner reports whether a recorded run is one of the cheap
// pre-suite stages (fmt, clippy, the whole-workspace check). Tests about
// SUITE scoping filter these out: they are workspace-wide by design and would
// otherwise read as an extra crate run.
func isQualityRunner(r Runner) bool {
	if r.Cmd == golangciLint {
		return true
	}
	if len(r.Args) == 0 {
		return false
	}
	if r.Cmd == "go" {
		return r.Args[0] == "vet"
	}
	if r.Cmd != "cargo" {
		return false
	}
	return r.Args[0] == "fmt" || r.Args[0] == "clippy" || r.Args[0] == "check"
}
