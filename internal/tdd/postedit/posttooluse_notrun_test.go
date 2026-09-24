package postedit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #820, from borld's forge_solver: an edit to src/spin/mod.rs ran
// `cargo nextest run -p forge_solver --lib -E test(/^spin::/)`, four unit
// tests passed, and the hook printed a plain green. The tests that exercise
// that module live in the crate's `--test integration` binary, which the
// scoped run never built. The edit hook stays fast and keeps the scoped run;
// what it may not do is let that run read as the crate's verdict. Every
// integration target the run left out is named NOT RUN on the gate line, in
// the words the commit gate already uses for a crate it did not test.

const nextestFourPassedOutput = "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.02s\n" +
	"    Starting 4 tests across 1 binary\n" +
	"        PASS [   0.004s] forge_solver spin::watch::tests::watch_holds\n" +
	"     Summary [   0.012s] 4 tests run: 4 passed, 0 skipped\n"

const notRunIntegrationTargets = "NOT RUN — --test integration, --test soak not tested here"

// integrationCrate is a git-tracked crate with a unit-tested module under
// src/ and two integration test targets: tests/integration.rs, which cargo
// discovers by convention, and a [[test]] declared at a path cargo would
// never guess. Both have to be named, so the list must come from cargo's own
// reading of the manifest and not from a directory listing.
func integrationCrate(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever cargo is installed.
		t.Skip("cargo not on PATH; the crate's targets are read through cargo metadata")
	}
	useRealCargoHome(t)
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_solver\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"+
		"[[test]]\nname = \"soak\"\npath = \"checks/soak.rs\"\n")
	write(t, root, "src/lib.rs", "pub mod spin;\n")
	write(t, root, "src/spin/mod.rs", "pub fn advect(x: f64) -> f64 { x * 0.5 }\n"+
		"#[cfg(test)]\nmod tests {\n    #[test]\n    fn halves() { assert_eq!(super::advect(2.0), 1.0); }\n}\n")
	write(t, root, "tests/integration.rs", "#[test]\nfn spin_advection() { assert_eq!(forge_solver::spin::advect(4.0), 2.0); }\n")
	write(t, root, "checks/soak.rs", "#[test]\nfn soak() { assert!(forge_solver::spin::advect(1.0) < 1.0); }\n")
	withNextest(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "src/spin/mod.rs", "pub fn advect(x: f64) -> f64 { x / 2.0 }\n"+
		"#[cfg(test)]\nmod tests {\n    #[test]\n    fn halves() { assert_eq!(super::advect(2.0), 1.0); }\n}\n")
	return root
}

const spinScopedRun = "cargo nextest run -p forge_solver --lib -E test(/^spin::/)"

// The foreground path: the scoped run's green names both integration targets
// it did not run, in the order cargo's target names sort.
func TestPostEdit_SrcEditInACrateWithIntegrationTargets_NamesThemNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := integrationCrate(t)
	var seen []string
	run := scriptedRunner(t, &seen, map[string]SuiteResult{
		spinScopedRun: {Passed: true, Output: nextestFourPassedOutput, Duration: time.Second},
	})

	got := PostEdit(postPayload("Edit", filepath.Join(root, "src", "spin", "mod.rs")), run)

	if !strings.Contains(got, "gate: "+spinScopedRun+" in ") || !strings.Contains(got, "green (4 passed") {
		t.Fatalf("setup: want the scoped run's green, got: %s", got)
	}
	if !strings.Contains(got, notRunIntegrationTargets) {
		t.Fatalf("a scoped --lib green must name the integration targets it did not run (%q), got: %s",
			notRunIntegrationTargets, got)
	}
}

// The deferred path the real hook takes: the same run as detached build and
// run phases reports the same NOT RUN clause.
func TestPostEditDeferred_SrcEditInACrateWithIntegrationTargets_NamesThemNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := integrationCrate(t)
	scriptedPhases(t, map[string]scriptedPhase{
		spinScopedRun + " --no-run": {out: &PhaseOutcome{ExitCode: 0}},
		spinScopedRun:               {out: &PhaseOutcome{ExitCode: 0}, log: nextestFourPassedOutput},
	})

	got := PostEdit(postPayload("Edit", filepath.Join(root, "src", "spin", "mod.rs")), fakeRun(true, "the foreground runner must not be used"))

	if !strings.Contains(got, "green (4 passed") {
		t.Fatalf("setup: want the scoped run's green, got: %s", got)
	}
	if !strings.Contains(got, notRunIntegrationTargets) {
		t.Fatalf("a deferred scoped --lib green must name the integration targets it did not run (%q), got: %s",
			notRunIntegrationTargets, got)
	}
}

// The harvest: a run phase that finished after its own hook returned is
// judged by the next hook, and its green carries the same clause.
func TestPostEditHarvest_SrcEditInACrateWithIntegrationTargets_NamesThemNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := integrationCrate(t)
	target := filepath.Join(root, "src", "spin", "mod.rs")
	scriptedPhases(t, map[string]scriptedPhase{})
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-post", Phase: "run", Dir: root, PID: 999,
		Started: time.Now().Add(-time.Minute),
		HeadSHA: headSHAFor(root), FileHash: sourceIdentity(root, target), File: target,
		Runner: strings.Fields(spinScopedRun),
	})
	job, _ := loadDeferredJob("sess-post", root)
	if err := os.WriteFile(job.Log, []byte(nextestFourPassedOutput), 0o600); err != nil {
		t.Fatal(err)
	}
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	got := PostEdit(postPayload("Edit", target), fakeRun(true, "the foreground runner must not be used"))

	if !strings.Contains(got, "green (4 passed") {
		t.Fatalf("setup: want the harvested run's green, got: %s", got)
	}
	if !strings.Contains(got, notRunIntegrationTargets) {
		t.Fatalf("a harvested scoped --lib green must name the integration targets it did not run (%q), got: %s",
			notRunIntegrationTargets, got)
	}
}

// A second source edit that passes the same count reads green-unconstrained,
// which is still a green, and carries the clause too.
func TestPostEdit_UnconstrainedGreenInACrateWithIntegrationTargets_NamesThemNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := integrationCrate(t)
	target := filepath.Join(root, "src", "spin", "mod.rs")
	var seen []string
	run := scriptedRunner(t, &seen, map[string]SuiteResult{
		spinScopedRun: {Passed: true, Output: nextestFourPassedOutput, Duration: time.Second},
	})
	PostEdit(postPayload("Edit", target), run)

	got := PostEdit(postPayload("Edit", target), run)

	if !strings.Contains(got, string(GreenUnconstrained)) {
		t.Fatalf("setup: want the second same-count green read as %s, got: %s", GreenUnconstrained, got)
	}
	if !strings.Contains(got, notRunIntegrationTargets) {
		t.Fatalf("an unconstrained scoped --lib green must name the integration targets it did not run (%q), got: %s",
			notRunIntegrationTargets, got)
	}
}

// A crate with no integration target has nothing the scoped run left out of
// that kind, and its green stays bare.
func TestPostEdit_SrcEditInACrateWithoutIntegrationTargets_StaysBare(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := integrationCrate(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge_solver\"\nversion = \"0.1.0\"\nedition = \"2021\"\nautotests = false\n")
	var seen []string
	run := scriptedRunner(t, &seen, map[string]SuiteResult{
		spinScopedRun: {Passed: true, Output: nextestFourPassedOutput, Duration: time.Second},
	})

	got := PostEdit(postPayload("Edit", filepath.Join(root, "src", "spin", "mod.rs")), run)

	if !strings.Contains(got, "green (4 passed") {
		t.Fatalf("setup: want the scoped run's green, got: %s", got)
	}
	if strings.Contains(got, "NOT RUN") {
		t.Fatalf("a crate with no integration target has none to name, got: %s", got)
	}
}

// The mechanical cache records the scoped run under its own argv, which is
// the fact that run proved. A lookup for a run that builds the integration
// binaries — the crate's whole suite, the merge gate's mechanical command, or
// one --test target — must miss: a scoped --lib green is not their green.
func TestPostEdit_ScopedLibGreenIsNotCachedAsTheCratesGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := integrationCrate(t)
	var seen []string
	run := scriptedRunner(t, &seen, map[string]SuiteResult{
		spinScopedRun: {Passed: true, Output: nextestFourPassedOutput, Duration: time.Second},
	})

	got := PostEdit(postPayload("Edit", filepath.Join(root, "src", "spin", "mod.rs")), run)

	if !strings.Contains(got, "green (4 passed") {
		t.Fatalf("setup: want the scoped run's green, got: %s", got)
	}
	h := worktreeStateHash(root)
	if h == "" {
		t.Fatal("setup: no state hash for the crate")
	}
	scoped := Runner{Cmd: "cargo", Args: strings.Fields(spinScopedRun)[1:]}
	if !mechCacheHit(mechKey(root, h, scoped)) {
		t.Fatalf("setup: the scoped green was not recorded under its own command %q", cmdString(scoped))
	}
	for _, wide := range []string{
		"cargo nextest run -p forge_solver",
		"cargo nextest run -p forge_solver --test integration",
		"cargo nextest run -p forge_solver --lib --test integration --test soak",
	} {
		r := Runner{Cmd: "cargo", Args: strings.Fields(wide)[1:]}
		if mechCacheHit(mechKey(root, h, r)) {
			t.Fatalf("the scoped --lib green answers a lookup for %q, which builds the integration binaries it never ran", wide)
		}
	}
}
