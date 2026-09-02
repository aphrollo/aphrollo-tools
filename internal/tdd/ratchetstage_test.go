package tdd

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitAddAll(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "add", "-A")
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
	cmd := exec.Command("git", "-c", "core.hooksPath=", "commit", "-q", "-m", "fixture", "--no-verify")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}
