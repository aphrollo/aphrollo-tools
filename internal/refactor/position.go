package refactor

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// symbolColumn returns the 0-based UTF-16 column of the first occurrence of
// symbol in line, matching the position encoding LSP servers expect. It errors
// if symbol is not present.
func symbolColumn(line, symbol string) (int, error) {
	b := strings.Index(line, symbol)
	if b < 0 {
		return 0, fmt.Errorf("symbol %q not found on line", symbol)
	}
	return len(utf16.Encode([]rune(line[:b]))), nil
}
