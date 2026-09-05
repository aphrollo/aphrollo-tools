package diff

import "testing"

// TestLines_ReportsRemovedAndAddedLinePositions is the shape a diff-relational
// ratchet law needs: which line was removed (and where, on the OLD side) and
// which was added (and where, on the NEW side), not a rendered hunk.
func TestLines_ReportsRemovedAndAddedLinePositions(t *testing.T) {
	before := "alpha\nbeta\ngamma\n"
	after := "alpha\nBETA\ngamma\n"

	ops := Lines(before, after)

	var removed, added []LineOp
	for _, o := range ops {
		switch o.Kind {
		case '-':
			removed = append(removed, o)
		case '+':
			added = append(added, o)
		}
	}
	if len(removed) != 1 || removed[0].Line != "beta" || removed[0].OldPos != 2 {
		t.Fatalf("removed = %+v, want one op {beta, oldPos 2}", removed)
	}
	if len(added) != 1 || added[0].Line != "BETA" || added[0].NewPos != 2 {
		t.Fatalf("added = %+v, want one op {BETA, newPos 2}", added)
	}
}

// TestLines_IdenticalTextProducesOnlyContextOps pins the boundary a caller
// relies on: no difference means no '-'/'+' op at all, only ' ' context —
// never an empty slice standing in for "nothing changed" (a hunk-regex law
// counts ops, not slice length, and an empty result would read as "no lines
// exist" rather than "nothing changed").
func TestLines_IdenticalTextProducesOnlyContextOps(t *testing.T) {
	text := "one\ntwo\n"
	ops := Lines(text, text)
	if len(ops) != 2 {
		t.Fatalf("Lines(x, x) = %d ops, want 2 (both context)", len(ops))
	}
	for _, o := range ops {
		if o.Kind != ' ' {
			t.Fatalf("Lines(x, x) produced a %q op, want only context", o.Kind)
		}
	}
}
