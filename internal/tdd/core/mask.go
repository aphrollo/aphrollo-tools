package core

import masklex "github.com/aphrollo/aphrollo-tools/internal/mask"

// Smell detectors run against a MASKED copy of the edit, so a pattern that
// only appears inside a string or a comment — `"call setTimeout"`, `// assert
// x == x`, a test description mentioning `.only` — never trips a blocking
// gate. Uniform masking across every detector is the single biggest
// false-positive fix in the port: the original hooks masked inconsistently, so
// any test that merely *mentioned* a smell in prose got blocked.
//
// The lexer itself lives in internal/mask, because the law engine needs the
// same answer for its `mask_strings` law option and internal/ratchet cannot
// import this package — the dependency runs the other way. Two lexers would
// be two sets of edge cases (escaped quotes, raw strings, `#` as a sigil
// rather than a comment), drifting apart one fix at a time.

// mask blanks BOTH strings and comments, treating `#` as a line comment. The
// bare helper defaults to `#`-as-comment so direct callers and the masker's own
// tests keep their existing behavior; the policy engine instead derives the
// `#` rule per file via maskTokens (see lang in policy.go), because `#` is a
// comment in Python/Ruby but a private-field sigil in JS/TS and absent in Go.
func mask(src string) string { return masklex.StringsAndComments(src) }

// maskTokens is internal/mask's lexer under this package's own name, so the
// detectors and their tests keep reading the way they always have.
func maskTokens(src string, blankStrings, blankComments, hashComment bool) string {
	return masklex.Tokens(src, blankStrings, blankComments, hashComment)
}
