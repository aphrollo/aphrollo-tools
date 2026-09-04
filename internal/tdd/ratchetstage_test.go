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

// The same fail-open shape is also issue #158: a law whose matcher kind the
// installed binary does not know made LoadLaws (and so ratchet.Check) fail
// outright, and skipping here did not just excuse that ONE law — it
// disarmed every OTHER law in the repo until the box reinstalled. The law
// tooling failing to even START is a different offence from a single
// unreadable file (nothing here names one path to retry), but it deserves
// the same verdict: a gate that cannot read its own laws is not a gate, so
// this blocks too, with a remedy a reader can act on.
func TestRatchetStage_BlocksAndNamesARemedyWhenALawFileIsUnparseable(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "future.toml"), `
name = "future"
description = "a matcher kind this binary does not know"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "bench-metric-ceiling-not-yet-invented"
`)
	gitAddAll(t, root)

	res := ratchetStage("precommit", root)
	if !res.Blocked {
		t.Fatalf("law tooling that cannot parse a law must block, not skip: %+v", res)
	}
	if !strings.Contains(res.Message, "unknown matcher kind") {
		t.Errorf("message %q does not name the parse failure", res.Message)
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
