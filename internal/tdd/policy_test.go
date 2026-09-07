package tdd

import (
	"strings"
	"testing"
)

func TestActionFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		c    category
		p    phase
		want Action
	}{
		{smellCat, editPhase, Block},         // smells always block
		{smellCat, commitPhase, Block},       //
		{suppressionCat, editPhase, Warn},    // suppression is advisory at edit
		{suppressionCat, commitPhase, Block}, // and a hard wall at commit
	}
	for _, c := range cases {
		if got := actionFor(c.c, c.p); got != c.want {
			t.Errorf("actionFor(%v, %v) = %v, want %v", c.c, c.p, got, c.want)
		}
	}
}

func TestEvaluate_MostSevereWins(t *testing.T) {
	t.Parallel()
	warnPol := policy{
		name: "warner", category: suppressionCat, reason: "warn",
		hit: func(v view) bool { return true },
	}
	blockPol := policy{
		name: "blocker", category: smellCat, reason: "block",
		hit: func(v view) bool { return true },
	}
	missPol := policy{
		name: "miss", category: smellCat, reason: "miss",
		hit: func(v view) bool { return false },
	}

	// A Block outranks a Warn regardless of slice order.
	if d := evaluate("x", []policy{warnPol, blockPol}, editPhase, defaultLang); d.Action != Block || d.Reason != "block" {
		t.Fatalf("warn-then-block: got %+v, want Block/block", d)
	}
	if d := evaluate("x", []policy{blockPol, warnPol}, editPhase, defaultLang); d.Action != Block || d.Reason != "block" {
		t.Fatalf("block-then-warn: got %+v, want Block/block", d)
	}
	// Only a suppression hits → Warn at edit, Block at commit.
	if d := evaluate("x", []policy{missPol, warnPol}, editPhase, defaultLang); d.Action != Warn {
		t.Fatalf("edit suppression: got %+v, want Warn", d)
	}
	if d := evaluate("x", []policy{missPol, warnPol}, commitPhase, defaultLang); d.Action != Block {
		t.Fatalf("commit suppression: got %+v, want Block", d)
	}
	// Nothing hits → Allow.
	if d := evaluate("x", []policy{missPol}, editPhase, defaultLang); d.Action != Allow {
		t.Fatalf("no hit: got %+v, want Allow", d)
	}
}

// TestEvaluate_ViewSelection proves a policy reading the code view does not see
// comment text, while one reading the directives view does — the split that
// lets smells ignore prose and suppressions read comments.
func TestEvaluate_ViewSelection(t *testing.T) {
	t.Parallel()
	codeReader := policy{
		name: "code", category: smellCat, reason: "code",
		hit: func(v view) bool { return strings.Contains(v.code, "MARK") },
	}
	dirReader := policy{
		name: "dir", category: suppressionCat, reason: "dir",
		hit: func(v view) bool { return strings.Contains(v.directives, "MARK") },
	}
	src := "f() // MARK"
	// The marker lives only in a comment: invisible to code, visible to directives.
	if d := evaluate(src, []policy{codeReader}, editPhase, defaultLang); d.Action != Allow {
		t.Fatalf("code view saw comment text: %+v", d)
	}
	if d := evaluate(src, []policy{dirReader}, editPhase, defaultLang); d.Action != Warn {
		t.Fatalf("directives view missed comment text: %+v", d)
	}
}
