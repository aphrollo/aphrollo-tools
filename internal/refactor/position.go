package refactor

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// symbolColumn returns the 0-based UTF-16 column of symbol's occurrence in
// line, matching the position encoding LSP servers expect. It errors if symbol
// is not present, and errors on more than one occurrence — silently anchoring
// on the first (e.g. `x := x + x` or `foo(foo, foo)`) picks an arbitrary one of
// several candidates; the caller must disambiguate with --col instead. The
// error lists every occurrence's column so the caller knows what to pass.
func symbolColumn(line, symbol string) (int, error) {
	first := strings.Index(line, symbol)
	if first < 0 {
		return 0, fmt.Errorf("symbol %q not found on line", symbol)
	}
	cols := []int{len(utf16.Encode([]rune(line[:first])))}
	for next := first + len(symbol); ; {
		i := strings.Index(line[next:], symbol)
		if i < 0 {
			break
		}
		i += next
		cols = append(cols, len(utf16.Encode([]rune(line[:i]))))
		next = i + len(symbol)
	}
	if len(cols) > 1 {
		strs := make([]string, len(cols))
		for i, c := range cols {
			strs[i] = fmt.Sprintf("%d", c+1) // 1-based, matching --col
		}
		return 0, fmt.Errorf("symbol %q is ambiguous on this line: occurs at columns %s — pass --col to pick one",
			symbol, strings.Join(strs, ", "))
	}
	return cols[0], nil
}
