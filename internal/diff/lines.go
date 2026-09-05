package diff

// LineOp is one line-diff operation: unchanged (' '), removed ('-') or added
// ('+'), carrying the 1-based line number it occupies on the side(s) it
// exists on. It is Unified's own diffOps, exported as data for a caller that
// walks the operations itself (a diff-relational ratchet law's hunk
// predicates) rather than rendering them.
type LineOp struct {
	Kind           byte
	Line           string
	OldPos, NewPos int
}

// Lines computes the line-level diff between before and after — the same
// algorithm Unified renders — without the trailing-newline bookkeeping
// Unified layers on for display: a caller reading positions and text has no
// use for a synthetic "\ No newline at end of file" marker line.
func Lines(before, after string) []LineOp {
	beforeLines, _ := splitLines(before)
	afterLines, _ := splitLines(after)
	ops := diffOps(beforeLines, afterLines)
	out := make([]LineOp, len(ops))
	for i, o := range ops {
		out[i] = LineOp{Kind: o.kind, Line: o.line, OldPos: o.oldPos, NewPos: o.newPos}
	}
	return out
}
