package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func lawTree(t *testing.T, severity string) string { t.Helper(); return tddtest.LawTree(t, severity) }

func ratchetPayload(t *testing.T, tool, path string, fields map[string]any) []byte {
	t.Helper()
	return tddtest.RatchetPayload(t, tool, path, fields)
}

func TestRatchetAdvisoryDeniesAWriteThatIntroducesANewHit(t *testing.T) {
	root := lawTree(t, "deny")
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n",
	})

	d := RatchetAdvisory(raw)
	if d.Action != Block {
		t.Fatalf("action = %v, want Block (reason: %s)", d.Action, d.Reason)
	}
	for _, want := range []string{"nan-guard", "crates/a/src/lib.rs:2", "// nan-safe:"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("reason %q does not carry %q", d.Reason, want)
		}
	}
}

func TestRatchetAdvisoryAllowsAWriteThatAddsNothingNew(t *testing.T) {
	root := lawTree(t, "deny")
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "let a = x.clamp(0.0, 1.0);\nlet b = 1;\n",
	})
	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("action = %v (%s), want Allow — the baselined site is unchanged", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryAppliesAnEditsOldAndNewStringsToTheFileOnDisk(t *testing.T) {
	root := lawTree(t, "deny")
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	raw := ratchetPayload(t, "Edit", path, map[string]any{
		"old_string": "let a = x.clamp(0.0, 1.0);",
		"new_string": "let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);",
	})
	d := RatchetAdvisory(raw)
	if d.Action != Block || !strings.Contains(d.Reason, "lib.rs:2") {
		t.Fatalf("action = %v, reason = %q", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryAppliesMultiEditSequentially(t *testing.T) {
	root := lawTree(t, "deny")
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	// The second edit's old_string exists only once the first has run, so a
	// verdict naming y.clamp proves the two were applied in order. It ADDS a
	// hit rather than swapping one: the pre-edit judge refuses a rise, and a
	// swap that leaves the count alone is the commit gate's to catch.
	raw := ratchetPayload(t, "MultiEdit", path, map[string]any{
		"edits": []map[string]any{
			{"old_string": "let a", "new_string": "let z"},
			{"old_string": "let z = x.clamp(0.0, 1.0);", "new_string": "let z = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);"},
		},
	})
	d := RatchetAdvisory(raw)
	if d.Action != Block || !strings.Contains(d.Reason, "y.clamp") {
		t.Fatalf("action = %v, reason = %q", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryHonoursTheEscapeComment(t *testing.T) {
	root := lawTree(t, "deny")
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0); // nan-safe: literal bounds\n",
	})
	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("action = %v (%s) — an escaped site is not a hit", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryWarnsButNeverBlocksForAWarnLaw(t *testing.T) {
	root := lawTree(t, "warn")
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n",
	})
	d := RatchetAdvisory(raw)
	if d.Action != Warn || !strings.Contains(d.Reason, "nan-guard") {
		t.Fatalf("action = %v, reason = %q", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryIsSilentOutsideAnyLawsScope(t *testing.T) {
	root := lawTree(t, "deny")
	outside := filepath.Join(root, "tools", "x.rs")
	mustWrite(t, outside, "let a = 1;\n")
	raw := ratchetPayload(t, "Write", outside, map[string]any{"content": "let a = q.clamp(0.0, 1.0);\n"})
	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("action = %v (%s) — the file is in no law's scope", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryIsSilentInARepoWithNoLaws(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	mustWrite(t, path, "let a = 1;\n")
	raw := ratchetPayload(t, "Write", path, map[string]any{"content": "let a = q.clamp(0.0, 1.0);\n"})
	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("action = %v (%s)", d.Action, d.Reason)
	}
}

// A pre-edit run must never move a baseline: the tree it judged is hypothetical.
func TestRatchetAdvisoryNeverRewritesABaseline(t *testing.T) {
	root := lawTree(t, "deny")
	baselinePath := filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")
	before, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	raw := ratchetPayload(t, "Write", path, map[string]any{"content": "let a = 1;\n"})
	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("action = %v (%s)", d.Action, d.Reason)
	}
	after, err := os.ReadFile(baselinePath)
	if err != nil || string(after) != string(before) {
		t.Errorf("baseline moved at pre-edit time: %q -> %q (%v)", before, after, err)
	}
}

func mustWrite(t *testing.T, path, content string) { t.Helper(); tddtest.MustWrite(t, path, content) }

// A Write that CREATES a file routinely names a directory that does not exist
// yet, and every git question asked from a missing directory fails — so the
// law engine read "no repo" and let the write through. The first file of a
// new module is exactly where a new offence lands.
func TestRatchetAdvisoryDeniesAWriteIntoADirectoryThatDoesNotExistYet(t *testing.T) {
	root := lawTree(t, "deny")
	path := filepath.Join(root, "crates", "new", "src", "lib.rs")
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("setup: %s must not exist yet (%v)", filepath.Dir(path), err)
	}
	// A line the baseline does not already carry: the baseline's identity is
	// the offending text, so repeating the recorded one is not a new hit
	// wherever it lands.
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "let b = y.clamp(0.0, 1.0);\n",
	})

	d := RatchetAdvisory(raw)
	if d.Action != Block {
		t.Fatalf("action = %v, want Block (reason: %s)", d.Action, d.Reason)
	}
	for _, want := range []string{"nan-guard", "crates/new/src/lib.rs:1"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("reason %q does not carry %q", d.Reason, want)
		}
	}
}

// The walk up must stop at the repo, not climb out of it: a path outside any
// repo has no laws to answer to, however many ancestors exist above it.
func TestRatchetAdvisoryAllowsAWriteOutsideAnyRepo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no", "repo", "here", "lib.rs")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "let a = x.clamp(0.0, 1.0);\n",
	})
	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("action = %v (%s), want Allow — no repo, no laws", d.Action, d.Reason)
	}
}

// TestRatchetStage_PassesHeadAsTheBase proves the commit gate always judges
// a diff-scoped law (symbol-removed) against the commit it is about to
// land on top of, for both shapes of commit this stage runs under.
func TestRatchetStage_PassesHeadAsTheBase(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)

	var got ratchet.Options
	original := ratchetCheckFn
	t.Cleanup(func() { ratchetCheckFn = original })
	ratchetCheckFn = func(opts ratchet.Options) (ratchet.Result, error) {
		got = opts
		return ratchet.Result{}, nil
	}

	ratchetStage("precommit", root)
	if got.Base != "HEAD" {
		t.Errorf("precommit: Options.Base = %q, want %q", got.Base, "HEAD")
	}

	got = ratchet.Options{}
	ratchetStage("premergecommit", root)
	if got.Base != "HEAD" {
		t.Errorf("premergecommit: Options.Base = %q, want %q", got.Base, "HEAD")
	}
}
