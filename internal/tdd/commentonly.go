package tdd

import (
	"path/filepath"
	"strings"
)

// A Go or Rust diff that never leaves a comment cannot change compiled behaviour:
// nothing outside a comment moved, so the build would produce the identical
// artifact and the suite would exercise the identical code path it already
// proved. Issue #723 is the same waste docsonly.go already refused for a
// pure-prose change (a re-pointed doc comment triggering the crate's full
// clippy/check/suite, 380-490s on this box) but for the far more common
// shape: a comment edit sitting INSIDE a source file that also carries real
// code, where the file's Kind is Source and the whole-commit docsOnly check
// can never fire.
//
// The comparison is TOKEN-level, not line-shape: "every changed line starts
// with `//`" would wave through a line that only LOOKS like a comment (a
// `//` opened inside a string literal) and would not know that `#[doc =
// "..."]` is an attribute, not a comment. For Rust, commentOnlyChange masks
// each image's comments with the same lexer the ratchet/suppression
// detectors already trust (internal/mask, via maskTokens) and compares what
// is left.

// commentOnlySource reports whether repoRoot's staged set can skip the build
// and suite for the SAME reason docsOnly can, extended to Go and Rust source:
// no test file is staged at all, and every staged Source file is a `.go` or
// `.rs` file whose diff changes no token outside a comment (see
// commentOnlySourceFile). A staged set with no Source file at all is not this
// path's business — that is docsOnly's — so this reports false whenever srcs
// is empty, leaving the caller to try docsOnly first.
func commentOnlySource(repoRoot string) bool {
	staged := stagedFiles(repoRoot)
	if len(staged) == 0 {
		return false
	}
	tests, srcs := splitKinds(staged)
	if len(tests) > 0 || len(srcs) == 0 {
		return false
	}
	for _, s := range srcs {
		if !commentOnlySourceFile(repoRoot, s) {
			return false
		}
	}
	return true
}

// commentOnlySourceFile reads one staged Source file's base and index blobs
// and hands them to commentOnlyChange (commentonly_go.go), the per-file rule
// CI's `gate classify-diff` asks too. A file whose base or index blob cannot
// be read — a newly added file, a deleted or otherwise unreadable index
// entry — answers false: an unprovable comparison must never be read as a
// safe one.
func commentOnlySourceFile(repoRoot, path string) bool {
	pre, err := git(repoRoot, "show", stagedBaseRev(repoRoot)+":"+path)
	if err != nil {
		return false
	}
	post, err := git(repoRoot, "show", ":"+path)
	if err != nil {
		return false
	}
	return commentOnlyChange(path, pre, post)
}

// commentOnlyRustFile is commentOnlySourceFile restricted to a `.rs` file.
// The Rust rule itself -- token-identical outside comments AND no fenced
// doc example anywhere in the file -- lives in commentOnlyChange. The fence
// is scoped to the whole file rather than the block that changed: a doc
// comment (///, //!, /** */, /*! */) feeds a doctest, rustdoc compiles and
// runs a fenced block from exactly these forms, and isolating the touched
// block would need a second line-level diff on top of the token comparison
// for a check whose only job is to fail safe.
func commentOnlyRustFile(repoRoot, path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".rs" && commentOnlySourceFile(repoRoot, path)
}

// commentOnlyDiff reports whether pre and post -- a Rust file's content
// before and after the staged edit -- carry the identical non-comment token
// stream. Comments are blanked with the shared lexer (maskTokens, blanking
// `//` and `/* */` only -- Rust's `#` opens an attribute, never a comment,
// so hashComment is false); the survivors are then split on whitespace so an
// edit that only changed how much whitespace or how many comment BYTES sat
// between two tokens (a comment shrinking or growing changes the masked
// string's length even though nothing it blanked was code) does not read as
// a token change of its own.
func commentOnlyDiff(pre, post string) bool {
	preTokens := strings.Fields(maskTokens(pre, false, true, false))
	postTokens := strings.Fields(maskTokens(post, false, true, false))
	if len(preTokens) != len(postTokens) {
		return false
	}
	for i, t := range preTokens {
		if t != postTokens[i] {
			return false
		}
	}
	return true
}

// hasFencedDocComment reports whether content carries a doc comment (`///`,
// `//!`, or a `/** */`/`/*! */` block) with a fenced triple-backtick code
// block anywhere in it -- see commentOnlyRustFile for why the scope is the
// whole file rather than just the edited block.
func hasFencedDocComment(content string) bool {
	inBlockDoc := false
	for raw := range strings.SplitSeq(content, "\n") {
		t := strings.TrimSpace(raw)
		switch {
		case inBlockDoc:
			if strings.Contains(raw, "```") {
				return true
			}
			if strings.Contains(raw, "*/") {
				inBlockDoc = false
			}
		case strings.HasPrefix(t, "///") || strings.HasPrefix(t, "//!"):
			if strings.Contains(raw, "```") {
				return true
			}
		case strings.HasPrefix(t, "/**") || strings.HasPrefix(t, "/*!"):
			if strings.Contains(raw, "```") {
				return true
			}
			if !strings.Contains(t[3:], "*/") {
				inBlockDoc = true
			}
		}
	}
	return false
}
