// Package diff renders deterministic unified diffs from before/after text. It
// is the VISIBLE output of the refactor tool: a dry-run rename prints these so
// a human or agent sees exactly what would change, losslessly.
package diff

import (
	"fmt"
	"strings"
)

// context is the number of unchanged lines shown around each change, matching
// the conventional unified-diff default.
const context = 3

// Unified renders a unified diff transforming before into after for the file at
// path. It returns "" when before == after. Inputs are treated as
// newline-terminated; a missing final newline is not specially annotated.
func Unified(path, before, after string) string {
	ops := diffOps(splitLines(before), splitLines(after))

	changed := false
	for _, o := range ops {
		if o.kind != ' ' {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}

	include := make([]bool, len(ops))
	for k, o := range ops {
		if o.kind == ' ' {
			continue
		}
		lo, hi := k-context, k+context
		if lo < 0 {
			lo = 0
		}
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		for i := lo; i <= hi; i++ {
			include[i] = true
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	for k := 0; k < len(ops); {
		if !include[k] {
			k++
			continue
		}
		s := k
		for k < len(ops) && include[k] {
			k++
		}
		writeHunk(&b, ops[s:k])
	}
	return b.String()
}

// op is a single line operation produced by the line diff. oldPos/newPos are
// the 1-based line numbers the op occupies in the old/new files.
type op struct {
	kind           byte // ' ', '-', or '+'
	line           string
	oldPos, newPos int
}

func writeHunk(b *strings.Builder, hunk []op) {
	oldCount, newCount := 0, 0
	for _, o := range hunk {
		if o.kind != '+' {
			oldCount++
		}
		if o.kind != '-' {
			newCount++
		}
	}
	// git's convention for a zero-line range is to point at the line BEFORE the
	// change (e.g. a pure insertion at the top is "-0,0", not "-1,0"), so step the
	// start back by one when its count is zero.
	oldStart, newStart := hunk[0].oldPos, hunk[0].newPos
	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, o := range hunk {
		b.WriteByte(o.kind)
		b.WriteString(o.line)
		b.WriteByte('\n')
	}
}

// splitLines splits newline-terminated text into lines, dropping the trailing
// empty element produced by a final newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// maxLCSCells bounds the longest-common-subsequence dynamic-programming table.
// The table is len(midA)·len(midB) ints, so an unbounded matrix on a
// multi-thousand-line file would allocate hundreds of MB on every rename diff.
// Once the changed region (after trimming the common head and tail) would exceed
// this many cells, diffOps falls back to a coarse delete-all/add-all block:
// still LOSSLESS (every old and new line is shown), just not minimal. 8M cells ≈
// 64 MB, generous against real edits but finite. Common edits trim to a tiny
// middle and never reach this.
const maxLCSCells = 8 << 20

// diffOps computes a line-level diff. It first strips the common prefix and
// suffix (emitted as context) so the expensive LCS runs only on the genuinely
// changed middle — which both bounds memory for the typical localized edit and
// keeps the diff minimal. The middle is diffed via an LCS table, or, if that
// table would exceed maxLCSCells, via a coarse full-replacement fallback.
func diffOps(a, b []string) []op {
	var ops []op
	oldNo, newNo := 1, 1
	emit := func(kind byte, line string) {
		ops = append(ops, op{kind: kind, line: line, oldPos: oldNo, newPos: newNo})
		switch kind {
		case ' ':
			oldNo++
			newNo++
		case '-':
			oldNo++
		case '+':
			newNo++
		}
	}

	// Common prefix.
	lo := 0
	for lo < len(a) && lo < len(b) && a[lo] == b[lo] {
		emit(' ', a[lo])
		lo++
	}
	// Common suffix (not overlapping the prefix).
	ea, eb := len(a), len(b)
	for ea > lo && eb > lo && a[ea-1] == b[eb-1] {
		ea--
		eb--
	}

	midA, midB := a[lo:ea], b[lo:eb]
	if len(midA)*len(midB) > maxLCSCells {
		// Coarse fallback: delete every changed-region old line, then add every
		// changed-region new line. Lossless, bounded memory, non-minimal.
		for _, l := range midA {
			emit('-', l)
		}
		for _, l := range midB {
			emit('+', l)
		}
	} else {
		emitLCS(midA, midB, emit)
	}

	// Common suffix, emitted as context.
	for k := ea; k < len(a); k++ {
		emit(' ', a[k])
	}
	return ops
}

// emitLCS diffs a and b via a longest-common-subsequence table, calling emit in
// order with ' ', '-', and '+' ops. Caller bounds len(a)·len(b).
func emitLCS(a, b []string, emit func(kind byte, line string)) {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			emit(' ', a[i])
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			emit('-', a[i])
			i++
		default:
			emit('+', b[j])
			j++
		}
	}
	for ; i < n; i++ {
		emit('-', a[i])
	}
	for ; j < m; j++ {
		emit('+', b[j])
	}
}
