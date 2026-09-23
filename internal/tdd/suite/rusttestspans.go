package suite

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// This file answers one question about a Rust source file, for the edit
// ledger (editledger.go): which of its bytes are TEST code and which are
// production. Test code is every item carrying `#[cfg(test)]` — normally the
// inline `mod tests { ... }` — or the whole file when a `#[cfg(test)]` module
// declaration mounts it (a sibling `widget_tests.rs` behind `#[path]`).
//
// The package's shared masker (internal/mask) cannot be used here: it reads
// every `'` as a string delimiter, and a Rust lifetime (`&'a str`) would then
// swallow the code up to the next quote, braces included. The lexer below
// knows Rust's own literal shapes: lifetimes, char and byte literals, raw
// strings with any number of `#`, and nested block comments.
//
// Every answer is a hash of a NORMALISED view: comments and layout are
// dropped, so a rustfmt pass or a reworded comment is not a change, while a
// string literal is kept byte for byte because changing one is. Anything the
// lexer cannot close (an unterminated literal, an unbalanced brace) refuses
// the whole file: the ledger then records the edit as unknown, which no proof
// can be built on.

// ambiguousTest is the hash recorded for a test name that appears twice in one
// file (two inner modules each with a `fn same()`): cargo's report names the
// failing one by module path, which this map cannot tell apart, so neither
// counts as identified.
const ambiguousTest = "ambiguous"

// rustSplit is one file's test/production split: the hash of the normalised
// production text ("" when there is none), the hash of all test code together
// ("" when there is none), the hash of the test code outside every #[test] fn
// (the helpers, imports and fixtures a test leans on), and each #[test] fn's
// own normalised hash by name.
type rustSplit struct {
	Prod    string
	Region  string
	Support string
	Tests   map[string]string
}

// Byte classes the lexer assigns.
const (
	rsCode byte = iota
	rsLiteral
	rsComment
)

var (
	rustCfgTestRe  = regexp.MustCompile(`#\s*\[\s*cfg\s*\(\s*test\s*\)\s*\]`)
	rustTestAttrRe = regexp.MustCompile(`#\s*\[\s*(?:\w+\s*::\s*)*(?:test|rstest|test_case)\b`)
	rustFnNameRe   = regexp.MustCompile(`^\s*(?:pub\s*(?:\([^)]*\))?\s*)?(?:const\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s*(?:"[^"]*")?\s*)?fn\s+(\w+)`)
)

// splitRustTests splits src into production and test code. wholeFileIsTest
// marks a file that a #[cfg(test)] module declaration mounts: all of it is
// test code. ok is false when the file cannot be lexed or is unbalanced.
func splitRustTests(src string, wholeFileIsTest bool) (rustSplit, bool) {
	class, ok := rustLex(src)
	if !ok {
		return rustSplit{}, false
	}
	masked := rustMasked(src, class)
	if !rustBalanced(masked) {
		return rustSplit{}, false
	}
	spans := [][2]int{{0, len(src)}}
	if !wholeFileIsTest {
		if spans, ok = rustCfgTestSpans(masked); !ok {
			return rustSplit{}, false
		}
	}
	out := rustSplit{Tests: map[string]string{}}
	out.Prod = hashNonEmpty(rustNormalize(src, class, [][2]int{{0, len(src)}}, spans))
	out.Region = hashNonEmpty(rustNormalize(src, class, spans, nil))
	var testSpans [][2]int
	for _, m := range rustTestAttrRe.FindAllStringIndex(masked, -1) {
		if !inSpans(spans, m[0]) {
			continue
		}
		start := rustAttrsBefore(masked, m[0])
		fnAt := rustSkipAttrs(masked, m[0])
		name := rustFnNameRe.FindStringSubmatch(masked[fnAt:])
		if name == nil {
			continue
		}
		end := rustItemEnd(masked, m[0])
		if end < 0 {
			return rustSplit{}, false
		}
		testSpans = append(testSpans, [2]int{start, end + 1})
		h := hashNonEmpty(rustNormalize(src, class, [][2]int{{start, end + 1}}, nil))
		if _, dup := out.Tests[name[1]]; dup {
			h = ambiguousTest
		}
		out.Tests[name[1]] = h
	}
	out.Support = hashNonEmpty(rustNormalize(src, class, spans, testSpans))
	return out, true
}

// rustCfgTestSpans returns the byte range of every item carrying
// `#[cfg(test)]` in masked code, its other attributes included. ok is false
// when one of them never closes.
func rustCfgTestSpans(masked string) ([][2]int, bool) {
	var spans [][2]int
	for _, m := range rustCfgTestRe.FindAllStringIndex(masked, -1) {
		if inSpans(spans, m[0]) {
			continue
		}
		end := rustItemEnd(masked, m[1])
		if end < 0 {
			return nil, false
		}
		spans = append(spans, [2]int{rustAttrsBefore(masked, m[0]), end + 1})
	}
	return spans, true
}

// rustMountsAsTest reports whether declFile's source mounts target (absolute)
// through a `#[path]` module declaration that carries `#[cfg(test)]`.
func rustMountsAsTest(declFile, declSrc, target string) bool {
	class, ok := rustLex(declSrc)
	if !ok {
		return false
	}
	spans, ok := rustCfgTestSpans(rustMasked(declSrc, class))
	if !ok {
		return false
	}
	for _, m := range pathModDeclRe.FindAllStringSubmatchIndex(declSrc, -1) {
		attr := strings.TrimSpace(declSrc[m[2]:m[3]])
		mounted := filepath.Join(filepath.Dir(declFile), filepath.FromSlash(attr))
		if mountKey(mounted) == mountKey(target) && inSpans(spans, m[6]) {
			return true
		}
	}
	return false
}

// rustLex classifies every byte of src as code, literal, or comment.
func rustLex(src string) ([]byte, bool) {
	n := len(src)
	class := make([]byte, n)
	mark := func(from, to int, c byte) {
		for k := from; k < to && k < n; k++ {
			class[k] = c
		}
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = n - i
			}
			mark(i, i+end, rsComment)
			i += end
		case c == '/' && i+1 < n && src[i+1] == '*':
			end, ok := rustBlockCommentEnd(src, i)
			if !ok {
				return nil, false
			}
			mark(i, end, rsComment)
			i = end
		case c == 'r' && rustRawStart(src, i):
			end, ok := rustRawEnd(src, i)
			if !ok {
				return nil, false
			}
			mark(i, end, rsLiteral)
			i = end
		case c == '"':
			end, ok := rustQuotedEnd(src, i)
			if !ok {
				return nil, false
			}
			mark(i, end, rsLiteral)
			i = end
		case c == '\'':
			if end := rustCharEnd(src, i); end > 0 {
				mark(i, end, rsLiteral)
				i = end
				continue
			}
			i++ // a lifetime or a loop label: code
		default:
			i++
		}
	}
	return class, true
}

func rustBlockCommentEnd(src string, i int) (int, bool) {
	depth := 0
	for j := i; j+1 < len(src); j++ {
		switch {
		case src[j] == '/' && src[j+1] == '*':
			depth++
			j++
		case src[j] == '*' && src[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1, true
			}
		}
	}
	return 0, false
}

// rustRawStart reports whether the `r` at i opens a raw string (`r"`, `r#"`,
// `br"`, `cr#"`), and not a raw identifier (`r#type`) or an ordinary name.
func rustRawStart(src string, i int) bool {
	if i > 0 && isIdentByte(src[i-1]) {
		if (src[i-1] != 'b' && src[i-1] != 'c') || (i > 1 && isIdentByte(src[i-2])) {
			return false
		}
	}
	j := i + 1
	for j < len(src) && src[j] == '#' {
		j++
	}
	return j < len(src) && src[j] == '"'
}

func rustRawEnd(src string, i int) (int, bool) {
	j := i + 1
	hashes := 0
	for src[j] == '#' {
		hashes++
		j++
	}
	closer := "\"" + strings.Repeat("#", hashes)
	end := strings.Index(src[j+1:], closer)
	if end < 0 {
		return 0, false
	}
	return j + 1 + end + len(closer), true
}

func rustQuotedEnd(src string, i int) (int, bool) {
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case '"':
			return j + 1, true
		}
	}
	return 0, false
}

// rustCharEnd returns the end of a char or byte literal opening at i, or 0 when
// the quote is a lifetime or label (`'a`, `'outer:`).
func rustCharEnd(src string, i int) int {
	if i+1 >= len(src) {
		return 0
	}
	if src[i+1] == '\\' {
		// An escape: '\n', '\'', '\x7f', '\u{1F600}'.
		for j := i + 2; j < len(src) && j < i+14; j++ {
			if src[j] == '\'' && j > i+2 {
				return j + 1
			}
			if src[j] == '\n' {
				return 0
			}
		}
		return 0
	}
	_, size := utf8.DecodeRuneInString(src[i+1:])
	if i+1+size < len(src) && src[i+1+size] == '\'' {
		return i + 2 + size
	}
	return 0
}

// rustMasked blanks every literal and comment byte, keeping newlines, so the
// structural scans below see only code.
func rustMasked(src string, class []byte) string {
	b := []byte(src)
	for i := range b {
		if class[i] != rsCode && b[i] != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

func rustBalanced(masked string) bool {
	var stack []byte
	pairs := map[byte]byte{')': '(', ']': '[', '}': '{'}
	for i := 0; i < len(masked); i++ {
		switch c := masked[i]; c {
		case '(', '[', '{':
			stack = append(stack, c)
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != pairs[c] {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

// rustSkipAttrs steps from pos over whitespace and any run of `#[...]`
// attributes, returning where the item itself starts.
func rustSkipAttrs(masked string, pos int) int {
	for {
		for pos < len(masked) && isSpaceByte(masked[pos]) {
			pos++
		}
		if pos >= len(masked) || masked[pos] != '#' {
			return pos
		}
		open := pos + 1
		for open < len(masked) && isSpaceByte(masked[open]) {
			open++
		}
		if open >= len(masked) || masked[open] != '[' {
			return pos
		}
		close := rustMatchForward(masked, open)
		if close < 0 {
			return pos
		}
		pos = close + 1
	}
}

// rustAttrsBefore steps back from pos over whitespace and any attributes
// written ahead of it (`#[should_panic]` above `#[test]`), which belong to the
// same item.
func rustAttrsBefore(masked string, pos int) int {
	for {
		j := pos - 1
		for j >= 0 && isSpaceByte(masked[j]) {
			j--
		}
		if j < 0 || masked[j] != ']' {
			return pos
		}
		depth := 0
		k := j
		for ; k >= 0; k-- {
			if masked[k] == ']' {
				depth++
			} else if masked[k] == '[' {
				if depth--; depth == 0 {
					break
				}
			}
		}
		h := k - 1
		for h >= 0 && isSpaceByte(masked[h]) {
			h--
		}
		if k < 0 || h < 0 || masked[h] != '#' {
			return pos
		}
		pos = h
	}
}

// rustItemEnd returns the index of the last byte of the item whose attributes
// start at pos: its closing `}`, or the `;` of a body-less item. -1 when the
// item never closes.
func rustItemEnd(masked string, pos int) int {
	depth := 0
	for i := rustSkipAttrs(masked, pos); i < len(masked); i++ {
		switch masked[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ';':
			if depth == 0 {
				return i
			}
		case '{':
			if depth == 0 {
				return rustMatchForward(masked, i)
			}
		case '}':
			if depth == 0 {
				return -1
			}
		}
	}
	return -1
}

// rustMatchForward returns the index closing the bracket at open.
func rustMatchForward(masked string, open int) int {
	closer := map[byte]byte{'(': ')', '[': ']', '{': '}'}[masked[open]]
	depth := 0
	for i := open; i < len(masked); i++ {
		switch masked[i] {
		case masked[open]:
			depth++
		case closer:
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// rustNormalize renders the bytes of src inside keep and outside drop with
// comments and layout removed: a whitespace run survives only as one space
// between two identifier bytes, where dropping it would join two tokens.
// Literal bytes are kept verbatim.
func rustNormalize(src string, class []byte, keep, drop [][2]int) string {
	var b strings.Builder
	pending := false
	var last byte
	for _, k := range keep {
		for i := k[0]; i < k[1]; i++ {
			if inSpans(drop, i) {
				pending = true
				continue
			}
			c := src[i]
			if class[i] == rsComment || (class[i] == rsCode && isSpaceByte(c)) {
				pending = true
				continue
			}
			if pending && b.Len() > 0 && isIdentByte(last) && isIdentByte(c) {
				b.WriteByte(' ')
			}
			pending = false
			b.WriteByte(c)
			last = c
		}
		pending = true
	}
	return b.String()
}

func inSpans(spans [][2]int, i int) bool {
	for _, s := range spans {
		if i >= s[0] && i < s[1] {
			return true
		}
	}
	return false
}

func hashNonEmpty(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
