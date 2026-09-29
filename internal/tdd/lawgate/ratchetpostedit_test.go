package lawgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// The post-edit law judge reads the tree as the edit left it and names what
// the commit gate would refuse, so a lane meets the refusal at the edit that
// caused it instead of at `git commit`. Three laws kept refusing commits the
// pre-edit judge let through (issue #968), each for its own reason, and each
// has a test here: a removed test (a diff-scoped law, given no base before),
// a README example naming no verb (a registry entry nothing uses, which a
// one-file scan cannot see), and a new hit written by a shell command (which
// fires no pre-edit hook at all).

// removedTestsTree is a committed repo with one symbol-removed law over Go
// test files, a/a_test.go holding two tests and c/c_test.go holding a third.
func removedTestsTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "removed_tests.toml"), `
name = "removed_tests"
description = "A test gone from the tree needs a tombstone"
severity = "deny"

[scope]
include = ["**/*_test.go"]

[matcher]
kind = "symbol-removed"
pattern = "^func (Test[A-Za-z0-9_]*)\\("
`)
	mustWrite(t, filepath.Join(root, "a", "a_test.go"),
		"package a\n\nfunc TestA_one(t *testing.T) {}\n\nfunc TestA_two(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(root, "c", "c_test.go"), "package c\n\nfunc TestC_one(t *testing.T) {}\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

// An edit that deletes a test from its file is refused at commit by the
// symbol-removed law; the edit hook names it, with the tombstone form.
func TestEditLawRefusals_NamesATestTheEditRemoved(t *testing.T) {
	root := removedTestsTree(t)
	mustWrite(t, filepath.Join(root, "a", "a_test.go"), "package a\n\nfunc TestA_one(t *testing.T) {}\n")

	got := strings.Join(editLawRefusals(root, []string{"a/a_test.go"}), "\n")

	for _, want := range []string{"removed_tests: a/a_test.go", "// ratchet: removed_tests <name>: <why>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("refusals = %q, want them to carry %q", got, want)
		}
	}
}

// A test moved to a file the lane created is not removed: the commit gate
// finds its name at the tip, so the edit hook must too.
func TestEditLawRefusals_AdmitsATestMovedToANewFile(t *testing.T) {
	root := removedTestsTree(t)
	mustWrite(t, filepath.Join(root, "a", "a_test.go"), "package a\n\nfunc TestA_one(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(root, "a", "two_test.go"), "package a\n\nfunc TestA_two(t *testing.T) {}\n")

	if got := editLawRefusals(root, []string{"a/a_test.go"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none — the test still stands in a/two_test.go", got)
	}
}

// The law judges names over the whole tree: a name that still stands in an
// UNCHANGED file elsewhere is not removed, and the edit hook must not refuse
// what the commit gate lets through.
func TestEditLawRefusals_AdmitsARemovedNameThatStandsInAnUnchangedFile(t *testing.T) {
	root := removedTestsTree(t)
	mustWrite(t, filepath.Join(root, "a", "a_test.go"),
		"package a\n\nfunc TestA_one(t *testing.T) {}\n\nfunc TestA_two(t *testing.T) {}\n\nfunc TestC_one(t *testing.T) {}\n")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, "a", "a_test.go"),
		"package a\n\nfunc TestA_one(t *testing.T) {}\n\nfunc TestA_two(t *testing.T) {}\n")

	if got := editLawRefusals(root, []string{"a/a_test.go"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none — TestC_one still stands in c/c_test.go", got)
	}
}

// A tombstone in the edited file admits the removal.
func TestEditLawRefusals_AdmitsARemovalWithItsTombstone(t *testing.T) {
	root := removedTestsTree(t)
	mustWrite(t, filepath.Join(root, "a", "a_test.go"),
		"package a\n\n// ratchet: removed_tests TestA_two: its subject is gone\n\nfunc TestA_one(t *testing.T) {}\n")

	if got := editLawRefusals(root, []string{"a/a_test.go"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none — the tombstone admits TestA_two", got)
	}
}

// A README line naming a verb nothing dispatches is a registry entry nobody
// uses. A scan narrowed to the edited file never sees the dispatch side, so
// the pre-edit judge let it through and the commit refused it.
func TestEditLawRefusals_NamesARegistryEntryNothingUses(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "README.md"), "run aphrollo a\nrun aphrollo b\n")

	got := strings.Join(editLawRefusals(root, []string{"README.md"}), "\n")

	if !strings.Contains(got, "b is registered but nothing uses it") {
		t.Fatalf("refusals = %q, want the unused registry entry named", got)
	}
}

// A per-file law's new hit, judged on the file as it sits on disk: what a
// shell write leaves behind, which no pre-edit hook ever saw.
func TestEditLawRefusals_NamesANewHitWithItsEscape(t *testing.T) {
	root := lawTree(t, "deny")
	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	got := strings.Join(editLawRefusals(root, []string{"crates/a/src/lib.rs"}), "\n")

	for _, want := range []string{"nan-guard: crates/a/src/lib.rs:2", "// nan-safe:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("refusals = %q, want them to carry %q", got, want)
		}
	}
}

// A warn law never refuses a commit, so it is no would-be refusal.
func TestEditLawRefusals_IgnoresAWarnLaw(t *testing.T) {
	root := lawTree(t, "warn")
	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	if got := editLawRefusals(root, []string{"crates/a/src/lib.rs"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none from a warn law", got)
	}
}

// A file no law scopes costs nothing and says nothing.
func TestEditLawRefusals_SaysNothingForAFileNoLawScopes(t *testing.T) {
	root := lawTree(t, "deny")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x.clamp(0.0, 1.0)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := editLawRefusals(root, []string{"notes.txt"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none — no law scopes notes.txt", got)
	}
}

// Editing one twin of a co-change pair alone is what the commit gate refuses.
func TestEditLawRefusals_NamesATwinChangedWithoutItsTwin(t *testing.T) {
	root := twinRepo(t)
	mustWrite(t, filepath.Join(root, "a.go"), twinA("2"))

	got := strings.Join(editLawRefusals(root, []string{"a.go"}), "\n")

	if !strings.Contains(got, "twins: a.go") {
		t.Fatalf("refusals = %q, want the twin-marked a.go named", got)
	}
}

// The twin an earlier edit already changed is part of the commit made now,
// so the pair is no refusal.
func TestEditLawRefusals_AdmitsATwinWhosePairAlreadyChanged(t *testing.T) {
	root := twinRepo(t)
	mustWrite(t, filepath.Join(root, "b.go"), "package a\n\nfunc B() { _ = 1 }\n")
	mustWrite(t, filepath.Join(root, "a.go"), twinA("2"))

	if got := editLawRefusals(root, []string{"a.go"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none — b.go changed too", got)
	}
}

// A change-scoped refusal an earlier edit left in another file is that
// edit's to report, not this one's.
func TestEditLawRefusals_LeavesAnotherFilesChangeFindingOut(t *testing.T) {
	root := twinRepo(t)
	mustWrite(t, filepath.Join(root, "a.go"), twinA("2"))
	mustWrite(t, filepath.Join(root, "c.go"), "package a\n\nfunc C() {}\n")

	if got := editLawRefusals(root, []string{"c.go"}); len(got) != 0 {
		t.Fatalf("refusals = %q, want none for c.go — the unpaired twin is a.go's", got)
	}
}

// An edit that takes a verb's last use out of the dispatch leaves its README
// entry used by nothing, a refusal only the whole registry scope can see.
func TestEditLawRefusals_NamesARegistryEntryWhoseLastUseTheEditRemoved(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "README.md"), "run aphrollo a\nrun aphrollo b\n")
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\ncase \"b\":\n}\n")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\n}\n")

	got := strings.Join(editLawRefusals(root, []string{"cli.go"}), "\n")

	if !strings.Contains(got, "b is registered but nothing uses it") {
		t.Fatalf("refusals = %q, want the entry whose last use left named", got)
	}
}

// lawNamed loads root's law of that name.
func lawNamed(t *testing.T, root, name string) ratchet.Law {
	t.Helper()
	laws, err := ratchet.LoadLaws(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range laws {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("no law %q under %s", name, root)
	return ratchet.Law{}
}

// Keeping the hook fast: an edit that removed no test name from its file
// cannot have removed a test, so the whole-tip scan is not run at all.
func TestEditJudgePlan_SkipsTheRemovedTestLawWhenNoNameLeftTheFile(t *testing.T) {
	root := removedTestsTree(t)
	mustWrite(t, filepath.Join(root, "a", "a_test.go"),
		"package a\n\nfunc TestA_one(t *testing.T) {}\n\nfunc TestA_two(t *testing.T) {}\n\nfunc TestA_three(t *testing.T) {}\n")

	if _, run := newEditJudge(root, []string{"a/a_test.go"}).plan(lawNamed(t, root, "removed_tests")); run {
		t.Fatal("plan runs the removed-test law over an edit that removed no name")
	}
}

// Keeping the hook fast: a use-side edit that took no use out stays a scan
// of the edited file, which answers "used but not registered" alone.
func TestEditJudgePlan_NarrowsTheRegistryLawWhenNoUseLeft(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\ncase \"b\":\n}\n")

	opts, run := newEditJudge(root, []string{"cli.go"}).plan(lawNamed(t, root, "verbs"))

	if !run || len(opts.Files) != 1 || opts.Files[0] != "cli.go" {
		t.Fatalf("plan = %+v (run %v), want a scan of cli.go alone", opts, run)
	}
}
