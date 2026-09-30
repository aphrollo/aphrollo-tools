package ratchet

// Block comments are found by the file-level lexer that reads the file's row of
// the language table (mask.CommentsForFile): only a row with `//` comments is
// read this way for a `//`-comment law, since a per-line strip cannot see a
// block comment and `#` languages have none.

// blankBlockComments blanks, in place, the parts of code that the file-level
// lexer read as comment. code is each line already cut at its trailing line
// comment; lexed is the same file with every comment blanked to spaces, so a
// line inside a `/* ... */` block, or carrying one, is spaces where the block
// was. Only the bytes the per-line cut kept are taken from lexed: a line the
// lexer reads identically stays byte-for-byte what it was, so a baseline keyed
// on that line's content never moves. The lexer only ever replaces bytes with
// spaces, and maskStringLines hands back the raw lines should it ever not, so
// lexed lines up with code line for line and byte for byte.
func blankBlockComments(code, lexed []string) {
	for i := range code {
		code[i] = lexed[i][:len(code[i])]
	}
}
