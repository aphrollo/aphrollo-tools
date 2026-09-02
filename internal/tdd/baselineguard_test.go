package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// baselineRepo commits one counted baseline, then leaves `after` staged.
func baselineRepo(t *testing.T, path, before, after string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, path), before)
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, path), after)
	gitAddAll(t, root)
	return root
}

func TestBaselineGuardRejectsARaisedCeiling(t *testing.T) {
	root := baselineRepo(t, "crates/ratchet/tests/module_size_baseline.txt",
		"# header\ncrates/a.rs | 1048\n",
		"# header\ncrates/a.rs | 1049\n")

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a hand-raised ceiling must reject the commit")
	}
	for _, want := range []string{"baseline-rejected", "crates/a.rs", "1048 -> 1049", "escape comment"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

func TestBaselineGuardRejectsANewKey(t *testing.T) {
	root := baselineRepo(t, ".ratchet/baselines/nan-guard.txt",
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\n",
		"crates/a.rs | let a = x.clamp(0.0, 1.0);\ncrates/b.rs | let b = y.clamp(0.0, 1.0);\n")

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "crates/b.rs") {
		t.Fatalf("adding a key by hand must reject: %+v", res)
	}
}

func TestBaselineGuardAllowsLoweringRemovingAndHeaderEdits(t *testing.T) {
	cases := map[string][2]string{
		"lowered":       {"crates/a.rs | 900\n", "crates/a.rs | 650\n"},
		"removed":       {"crates/a.rs | 900\ncrates/b.rs | 10\n", "crates/b.rs | 10\n"},
		"header only":   {"# old note\ncrates/a.rs | 900\n", "# new note\ncrates/a.rs | 900\n"},
		"multiset drop": {"crates/a.rs | x.sin()\ncrates/a.rs | x.sin()\n", "crates/a.rs | x.sin()\n"},
	}
	for name, c := range cases {
		root := baselineRepo(t, "crates/ratchet/tests/x_baseline.txt", c[0], c[1])
		if res := baselineStage("precommit", root); res.Blocked {
			t.Errorf("%s must pass: %s", name, res.Message)
		}
	}
}

func TestBaselineGuardCountsIdenticalMultisetLines(t *testing.T) {
	root := baselineRepo(t, ".ratchet/baselines/nan-guard.txt",
		"crates/a.rs | v.clamp(0.0, 1.0)\n",
		"crates/a.rs | v.clamp(0.0, 1.0)\ncrates/a.rs | v.clamp(0.0, 1.0)\n")

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "1 -> 2") {
		t.Fatalf("a repeated identity line is one more occurrence: %+v", res)
	}
}

// A brand-new baseline file is a law being ADOPTED, reviewed as such — the
// rule is about a ceiling that already exists being raised.
func TestBaselineGuardAllowsABrandNewBaselineFile(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "a.txt"), "seed\n")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "new-law.txt"), "crates/a.rs | 5\n")
	gitAddAll(t, root)

	if res := baselineStage("precommit", root); res.Blocked {
		t.Fatalf("adopting a law must not be a raise: %s", res.Message)
	}
}

func TestBaselineGuardIgnoresFilesOutsideTheDeclaredGlobs(t *testing.T) {
	root := baselineRepo(t, "docs/notes.txt", "crates/a.rs | 1\n", "crates/a.rs | 99\n")
	if res := baselineStage("precommit", root); res.Blocked {
		t.Fatalf("only baseline files are guarded: %s", res.Message)
	}
}

func TestBaselineGuardHonoursTheWorkspaceGlobList(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "Cargo.toml"),
		"[workspace.metadata.aphrollo]\nbaselines = [\"guards/*.txt\"]\n")
	mustWrite(t, filepath.Join(root, "guards", "sizes.txt"), "crates/a.rs | 10\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "other.txt"), "crates/a.rs | 10\n")
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, "guards", "sizes.txt"), "crates/a.rs | 11\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "other.txt"), "crates/a.rs | 11\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "guards/sizes.txt") {
		t.Fatalf("a declared glob must be guarded: %+v", res)
	}
	if strings.Contains(res.Message, "other.txt") {
		t.Errorf("a declared list REPLACES the defaults: %s", res.Message)
	}
}

func TestBaselineGuardIsSilentWhenNoBaselineIsStaged(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	gitAddAll(t, root)
	if res := baselineStage("precommit", root); res.Blocked || res.Message != "" {
		t.Fatalf("result = %+v, want silence", res)
	}
}
