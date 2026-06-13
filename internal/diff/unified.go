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
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", hunk[0].oldPos, oldCount, hunk[0].newPos, newCount)
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

// diffOps computes a line-level diff via a longest-common-subsequence table.
func diffOps(a, b []string) []op {
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

	var ops []op
	i, j, oldNo, newNo := 0, 0, 1, 1
	emit := func(kind byte, line string) {
		o := op{kind: kind, line: line, oldPos: oldNo, newPos: newNo}
		switch kind {
		case ' ':
			oldNo++
			newNo++
		case '-':
			oldNo++
		case '+':
			newNo++
		}
		ops = append(ops, o)
	}
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
	return ops
}
