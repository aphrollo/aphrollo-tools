package precommit

import (
	"go/scanner"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
)

// commentOnlyChange is the one per-file comment-only decision, over a file's
// content before and after a change. Both the commit gate (index against its
// base, commentOnlySourceFile) and CI's `gate classify-diff` (head against
// the pull request's base) read their two blobs and ask it, so a rule
// tightened here reaches both paths at once. Anything but Go or Rust answers
// false: there is no token comparison for it, and an unprovable comparison
// must never read as a safe one.
func commentOnlyChange(path, pre, post string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return commentOnlyGoDiff(pre, post)
	case ".rs":
		// A doc comment carrying a fenced block feeds a doctest; see
		// commentOnlyRustFile's note on why the whole file sits out.
		return commentOnlyDiff(pre, post) && !hasFencedDocComment(pre) && !hasFencedDocComment(post)
	}
	return false
}

// commentOnlyGoDiff reports whether pre and post carry the identical Go token
// stream once ordinary comments are dropped. The comparison runs on
// go/scanner's own tokens, so a `//` inside a string literal is never read as
// a comment opener, and a comment that changes automatic semicolon insertion
// (a block comment gaining a newline after an identifier) shows up as the
// changed SEMICOLON token it is.
//
// Two kinds of comment are code and stay in the stream: a compiler, tool or
// linter directive (`//go:build`, `//go:embed`, `//line`, `//export`, a
// lint-suppression `//word:` and the rest of that family, plus the legacy
// `// +build`), and the cgo preamble, which is C source the toolchain
// compiles -- a file importing "C" is refused outright rather than guessing
// which comment is the preamble.
func commentOnlyGoDiff(pre, post string) bool {
	a, ok := goCodeTokens(pre)
	if !ok {
		return false
	}
	b, ok := goCodeTokens(post)
	if !ok {
		return false
	}
	return slices.Equal(a, b)
}

// goCodeTokens returns src's tokens with ordinary comments dropped and
// directive comments kept, each as "kind literal", and false for a source
// the scanner rejects or one that imports "C".
func goCodeTokens(src string) ([]string, bool) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	scanErrors := 0
	s.Init(file, []byte(src), func(token.Position, string) { scanErrors++ }, scanner.ScanComments)
	var out []string
	prev := token.ILLEGAL
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			if isGoDirectiveComment(lit) {
				out = append(out, tok.String()+" "+lit)
			}
			continue
		}
		if tok == token.STRING && lit == `"C"` && (prev == token.IMPORT || prev == token.LPAREN || prev == token.SEMICOLON) {
			return nil, false
		}
		out = append(out, tok.String()+" "+lit)
		prev = tok
	}
	return out, scanErrors == 0
}

// isGoDirectiveComment reports whether a comment's full text is one the
// toolchain or a linter reads: go/ast's own directive shape (`//line `,
// `//extern `, `//export `, `//[a-z0-9]+:[a-z0-9]`), a `/*line ` block, or
// a legacy `// +build` constraint.
func isGoDirectiveComment(c string) bool {
	if strings.HasPrefix(c, "/*line ") {
		return true
	}
	body, ok := strings.CutPrefix(c, "//")
	if !ok {
		return false
	}
	if strings.HasPrefix(body, " +build") {
		return true
	}
	for _, p := range []string{"line ", "extern ", "export "} {
		if strings.HasPrefix(body, p) {
			return true
		}
	}
	colon := strings.Index(body, ":")
	if colon <= 0 || colon+1 >= len(body) {
		return false
	}
	for i := 0; i <= colon+1; i++ {
		if i == colon {
			continue
		}
		b := body[i]
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') {
			return false
		}
	}
	return true
}
