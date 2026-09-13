package tdd

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

func gitAddAll(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command(gitBinary(), "add", "-A")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
}

func TestRatchetStageAllowsATreeAtItsBaseline(t *testing.T) {
	root := lawTree(t, "deny")
	addFixtures(t, root)
	gitAddAll(t, root)
	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("blocked a clean tree: %s", res.Message)
	}
}

func TestRatchetStageRejectsARegressionAndNamesTheLaw(t *testing.T) {
	root := lawTree(t, "deny")
	addFixtures(t, root)
	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	res := ratchetStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a deny law's regression must reject the commit")
	}
	for _, want := range []string{"nan-guard", "lib.rs:2"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

func TestRatchetStageIsSilentInARepoWithNoLaws(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	gitAddAll(t, root)
	res := ratchetStage("precommit", root)
	if res.Blocked || res.Message != "" {
		t.Fatalf("result = %+v, want a silent pass", res)
	}
}

// Editing the laws themselves is when their own fixtures must run: a rule
// changed without re-proving it can stop catching anything.
func TestRatchetStageProvesFixturesWhenALawFileIsStaged(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)
	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "catches nothing") {
		t.Fatalf("staging a law with no fixtures must reject: %+v", res)
	}

	base := filepath.Join(root, ".ratchet", "fixtures", "nan-guard")
	mustWrite(t, filepath.Join(base, "hit", "crates", "a", "src", "bare.rs"), "let b = x.clamp(0.0, 1.0);\n")
	mustWrite(t, filepath.Join(base, "expected.txt"), "crates/a/src/bare.rs:1\n")
	mustWrite(t, filepath.Join(base, "clean", "crates", "a", "src", "ok.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")
	gitAddAll(t, root)
	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("fixtured laws must pass: %s", res.Message)
	}
}

// A commit that touches no law file does not re-run the fixtures — the check
// over the tree is the per-commit cost, and it is milliseconds.
func TestRatchetStageSkipsFixturesWhenNoLawFileIsStaged(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, "crates", "a", "src", "other.rs"), "let c = 1;\n")
	gitAddAll(t, root)

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an unrelated commit must not pay for the unfixtured law: %s", res.Message)
	}
}

// The generated .ratchet/README.md cannot change any law's or fixture's
// verdict, so staging it alone must not re-run the fixtures stage — this
// law's own fixtures were never written, so if the stage fired here it
// would reject with "catches nothing" exactly as
// TestRatchetStageProvesFixturesWhenALawFileIsStaged does above.
func TestRatchetStage_SkipsFixturesWhenOnlyAGeneratedDocIsStaged(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "README.md"), "# generated\n")
	gitAddAll(t, root)

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("staging only the generated README must not re-run fixtures: %s", res.Message)
	}
}

// A scoped file the engine could not read (locked, permission-denied, any
// I/O error) leaves this commit's laws unproven, and the gate must never
// treat "could not judge" as "judged clean" — that is issue #164's
// caller-side gap: internal/ratchet already refuses a false-clean verdict
// for exactly this case (returns a *ratchet.ScanReadError instead of a
// silent drop), but this stage read ANY Check() error the same way, as
// "tooling problem, skip it and let the commit through". ratchetCheckFn is
// overridden rather than constructing a genuinely unreadable file on disk:
// an OS-level read-deny is not portably constructible from a test (see
// internal/ratchet/check_readerror_test.go's own reasoning) — this test's
// job is the CALLER's classification of the error Check hands back, not the
// scan that produces it.
func TestRatchetStage_BlocksWhenAScopedFileCannotBeRead(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)

	original := ratchetCheckFn
	t.Cleanup(func() { ratchetCheckFn = original })
	ratchetCheckFn = func(ratchet.Options) (ratchet.Result, error) {
		return ratchet.Result{}, &ratchet.ScanReadError{
			Path: "crates/a/src/lib.rs",
			Err:  errors.New("locked by another process"),
		}
	}

	res := ratchetStage("precommit", root)
	if !res.Blocked {
		t.Fatalf("an unreadable scoped file must block the commit, not skip it: %+v", res)
	}
	if !strings.Contains(res.Message, "crates/a/src/lib.rs") {
		t.Errorf("message %q does not name the unreadable file", res.Message)
	}
}

// ratchet: test_removed TestRatchetStage_BlocksAndNamesARemedyWhenALawFileIsUnparseable: superseded by TestRatchetStage_UnknownMatcherKindDoesNotBlockTheCommit and TestRatchetStage_UnknownMatcherKindIsLoggedAsAStandDown below — #440 changed an unknown matcher kind from a hard block to a skip-and-warn, so this test's own claim ("must block, not skip") became the bug it used to guard against.

// futureKindLaw is a law naming a matcher kind no binary in this tree
// compiles in — the shape a lane lands before every checkout on the box
// rebuilds from it (#439/#440).
const futureKindLaw = `
name = "future"
description = "a matcher kind this binary does not know"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "bench-metric-ceiling-not-yet-invented"
`

// This used to be issue #158's shape: a law whose matcher kind the installed
// binary does not know made LoadLaws (and so ratchet.Check) fail outright,
// and treating that as "tooling problem, skip it" did not just excuse that
// ONE law — it disarmed every OTHER law in the repo until the box
// reinstalled, so the old fix was to BLOCK the whole commit instead. #440 is
// that block itself turning into the SAME hazard one level up: the block
// fires in every checkout on the box the moment a lane's `.ratchet/laws/`
// commit reaches it, including checkouts on a lane that never asked for the
// new kind — so unknown-kind is not "law tooling could not start" (a
// malformed TOML) at all; it skips that ONE law and lets every OTHER law,
// deny or not, keep judging the commit.
func TestRatchetStage_UnknownMatcherKindDoesNotBlockTheCommit(t *testing.T) {
	root := lawTree(t, "deny")
	addFixtures(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "future.toml"), futureKindLaw)
	gitAddAll(t, root)

	res := ratchetStage("precommit", root)
	if res.Blocked {
		t.Fatalf("a law naming an unknown matcher kind must be SKIPPED, not block every other law in the repo: %s", res.Message)
	}
}

// The skip above must not be silent — #320 is exactly a guard that stops
// enforcing without anyone able to see it happened — so it is counted the
// same way every other stand-down in gate.log is (denyVerdictPrefixes'
// "standdown-" prefix).
func TestRatchetStage_UnknownMatcherKindIsLoggedAsAStandDown(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := lawTree(t, "deny")
	addFixtures(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "future.toml"), futureKindLaw)
	gitAddAll(t, root)

	ratchetStage("precommit", root)

	log := gateLogContent(t)
	if !strings.Contains(log, "standdown-unknown-matcher-kind:future") {
		t.Fatalf("skipping law %q for an unknown matcher kind must be recorded through appendGateLog, got:\n%s", "future", log)
	}
}

// A `[scope] changed = "staged"` law (co-change, hunk-regex) answers nothing
// without Options.StagedFiles — silently, since changedLawHits only leaves a
// Note, and ratchetStage used not to even print those. Wiring it in is what
// makes such a law real at a commit rather than decoration only the ratchet
// package's own unit tests (and `ratchet check` run by hand) ever exercise.
func TestRatchetStage_WiresStagedFilesSoADiffScopedLawActuallyFires(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "twins.toml"), `
name = "twins"
description = "a twin pair must change together"
severity = "deny"

[scope]
changed = "staged"
include = ["**/*.go"]

[matcher]
kind = "co-change"
`)
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n\n// twin: b.go#B\nfunc A() {}\n")
	mustWrite(t, filepath.Join(root, "b.go"), "package a\n\nfunc B() {}\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// Only a.go changes in this commit; its twin b.go does not — the law's
	// own fixtures are not re-proved here since .ratchet/ is untouched by
	// this second commit (see TestRatchetStageSkipsFixturesWhenNoLawFileIsStaged).
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n\n// twin: b.go#B\nfunc A() { println(1) }\n")
	gitAddAll(t, root)

	res := ratchetStage("precommit", root)
	if !res.Blocked {
		t.Fatalf("a.go changed but its twin b.go did not — the co-change law must block this commit: %+v", res)
	}
}

// addFixtures gives the fixture law the hit/clean pair every law owes.
func addFixtures(t *testing.T, root string) {
	t.Helper()
	base := filepath.Join(root, ".ratchet", "fixtures", "nan-guard")
	mustWrite(t, filepath.Join(base, "hit", "crates", "a", "src", "bare.rs"), "let b = x.clamp(0.0, 1.0);\n")
	mustWrite(t, filepath.Join(base, "expected.txt"), "crates/a/src/bare.rs:1\n")
	mustWrite(t, filepath.Join(base, "clean", "crates", "a", "src", "ok.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")
}

func commitAll(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command(gitBinary(), "-c", "core.hooksPath=", "commit", "-q", "-m", "fixture", "--no-verify")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}
