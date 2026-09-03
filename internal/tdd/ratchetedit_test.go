package tdd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// The pre-edit judge scored the WHOLE post-edit file against the baseline, so
// a file already carrying two hits refused every edit to it — including the
// edit that removed one of them. A refusal that cannot be satisfied pushes a
// builder into `sed` or a Python write, which is the one thing the gate must
// never cause. At edit time the comparison is the file against ITSELF as it
// stands on disk: only a RISE is refused, and only the added line is named.

// overBaselineTree is a repo whose one law has an EMPTY baseline and whose
// file already carries two offences — the shape the pre-edit judge got wrong.
func overBaselineTree(t *testing.T) (root, rel string) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), `
name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
escape = "// nan-safe:"
baseline = ".ratchet/baselines/nan-guard.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\.clamp\("
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "")
	rel = "crates/a/src/lib.rs"
	mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	return root, rel
}

func TestRatchetAdvisoryAllowsAnEditThatRemovesOneOfTwoHits(t *testing.T) {
	root, rel := overBaselineTree(t)
	raw := ratchetPayload(t, "Edit", filepath.Join(root, filepath.FromSlash(rel)), map[string]any{
		"old_string": "let b = y.clamp(0.0, 1.0);",
		"new_string": "let b = y;",
	})

	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("an edit that LOWERS the count must not be refused for the hit it leaves: %v %s", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryAllowsAnEditThatLeavesTheCountUnchanged(t *testing.T) {
	root, rel := overBaselineTree(t)
	raw := ratchetPayload(t, "Edit", filepath.Join(root, filepath.FromSlash(rel)), map[string]any{
		"old_string": "let a = x.clamp(0.0, 1.0);",
		"new_string": "let renamed = x.clamp(0.0, 1.0);",
	})

	if d := RatchetAdvisory(raw); d.Action != Allow {
		t.Fatalf("an edit that adds no hit must not be refused: %v %s", d.Action, d.Reason)
	}
}

func TestRatchetAdvisoryDeniesAnAddedHitAndNamesOnlyTheAddedLine(t *testing.T) {
	root, rel := overBaselineTree(t)
	raw := ratchetPayload(t, "Edit", filepath.Join(root, filepath.FromSlash(rel)), map[string]any{
		"old_string": "let b = y.clamp(0.0, 1.0);",
		"new_string": "let b = y.clamp(0.0, 1.0);\nlet c = z.clamp(0.0, 1.0);",
	})

	d := RatchetAdvisory(raw)
	if d.Action != Block {
		t.Fatalf("an edit that RAISES the count must be denied, got %v %s", d.Action, d.Reason)
	}
	if !strings.Contains(d.Reason, "let c = z.clamp(0.0, 1.0);") {
		t.Fatalf("the denial must name the added line, got %q", d.Reason)
	}
	for _, old := range []string{"let a = x.clamp(0.0, 1.0);", "let b = y.clamp(0.0, 1.0);"} {
		if strings.Contains(d.Reason, old) {
			t.Errorf("the denial must name ONLY the added line, but it carries %q", old)
		}
	}
}

func TestAddedHits_CountsWhatTheEditIntroduced(t *testing.T) {
	hit := func(key string, line int) ratchet.Hit {
		return ratchet.Hit{Law: "l", File: "f", Key: key, What: key, Line: line, Weight: 1}
	}
	keys := func(hits []ratchet.Hit) string {
		var out []string
		for _, h := range hits {
			out = append(out, h.Key)
		}
		return strings.Join(out, ",")
	}
	before := []ratchet.Hit{hit("a", 1), hit("b", 2)}
	after := []ratchet.Hit{hit("a", 1), hit("b", 2), hit("c", 3)}
	if got := keys(addedHits(before, after)); got != "c" {
		t.Fatalf("added = %q, want the one new key", got)
	}
	if got := keys(addedHits(after, before)); got != "" {
		t.Fatalf("a removal adds nothing, got %q", got)
	}
	// A repeated key is counted, not deduped: two identical offences are two,
	// and the SECOND one is the line the edit put there.
	added := addedHits([]ratchet.Hit{hit("a", 1)}, []ratchet.Hit{hit("a", 1), hit("a", 4)})
	if keys(added) != "a" || len(added) != 1 || added[0].Line != 4 {
		t.Fatalf("a second copy of an existing key is an addition at its own line, got %+v", added)
	}
}
