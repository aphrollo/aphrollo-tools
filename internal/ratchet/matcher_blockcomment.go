package ratchet

import (
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/mask"
)

// commentBlankerFor picks the lexer that blanks a file's comments — block
// comments included, which a per-line strip cannot see — with the same
// language rules maskerFor applies to strings. Only `//`-comment languages
// are read this way: `#` languages have no block comment to find.
func commentBlankerFor(file string) func(string, bool) string {
	if strings.ToLower(filepath.Ext(file)) == ".rs" {
		return func(src string, strs bool) string { return mask.RustTokens(src, strs, true) }
	}
	return func(src string, strs bool) string { return mask.Tokens(src, strs, true, false) }
}

// blankBlockComments blanks, in place, the parts of code that the file-level
// lexer read as comment. code is each line already cut at its trailing line
// comment; lexed is the same file with every comment blanked to spaces, so a
// line inside a `/* ... */` block, or carrying one, is spaces where the block
// was. Only the bytes the per-line cut kept are taken from lexed: a line the
// lexer reads identically stays byte-for-byte what it was, so a baseline keyed
// on that line's content never moves.
func blankBlockComments(code, lexed []string) {
	for i := range code {
		if i < len(lexed) && len(lexed[i]) >= len(code[i]) {
			code[i] = lexed[i][:len(code[i])]
		}
	}
}
