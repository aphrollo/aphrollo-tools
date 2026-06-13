// Package lsp holds Language Server Protocol types and the deterministic,
// host-tool-free operations we build on them (applying edits, rendering diffs).
package lsp

import (
	"fmt"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// Position is a zero-based LSP position. Character counts UTF-16 code units,
// per the LSP spec, not bytes or runes.
type Position struct {
	Line      int
	Character int
}

// Range is a half-open [Start, End) span of text.
type Range struct {
	Start Position
	End   Position
}

// TextEdit replaces the text in Range with NewText, as returned by a language
// server (e.g. inside a WorkspaceEdit from textDocument/rename).
type TextEdit struct {
	Range   Range
	NewText string
}

// ApplyEdits applies edits to src and returns the rewritten text. Edits are
// expressed in LSP coordinates against the ORIGINAL src; they are resolved to
// byte offsets up front and applied from the end of the buffer backwards so
// earlier offsets stay valid.
func ApplyEdits(src string, edits []TextEdit) (string, error) {
	type resolved struct {
		start, end int
		newText    string
	}
	res := make([]resolved, 0, len(edits))
	for _, e := range edits {
		start, err := byteOffset(src, e.Range.Start)
		if err != nil {
			return "", err
		}
		end, err := byteOffset(src, e.Range.End)
		if err != nil {
			return "", err
		}
		res = append(res, resolved{start: start, end: end, newText: e.NewText})
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].start > res[j].start })

	// res is sorted by start descending; res[i] sits after res[i+1]. Half-open
	// ranges may touch (next.end == cur.start) but must not overlap.
	for i := 0; i+1 < len(res); i++ {
		if res[i+1].end > res[i].start {
			return "", fmt.Errorf("overlapping edits: [%d,%d) and [%d,%d)",
				res[i+1].start, res[i+1].end, res[i].start, res[i].end)
		}
	}

	b := []byte(src)
	for _, r := range res {
		b = append(b[:r.start], append([]byte(r.newText), b[r.end:]...)...)
	}
	return string(b), nil
}

// byteOffset converts an LSP Position into a byte offset into src.
func byteOffset(src string, p Position) (int, error) {
	i, line := 0, 0
	for line < p.Line {
		nl := indexNewlineFrom(src, i)
		if nl < 0 {
			return 0, fmt.Errorf("line %d out of range", p.Line)
		}
		i = nl + 1
		line++
	}
	col := 0
	for col < p.Character {
		if i >= len(src) || src[i] == '\n' {
			return 0, fmt.Errorf("character %d out of range on line %d", p.Character, p.Line)
		}
		r, size := utf8.DecodeRuneInString(src[i:])
		col += len(utf16.Encode([]rune{r}))
		i += size
	}
	return i, nil
}

func indexNewlineFrom(s string, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}
