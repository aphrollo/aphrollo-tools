package precommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Go comment-only decisions, judged on the two blob images alone -- the
// shape both the commit gate (index against its base) and CI's
// `gate classify-diff` (head against the PR base) hand commentOnlyChange.

// A re-worded line comment and doc comment change no compiled token: the
// build is byte-identical, so the suite has nothing new to prove.
func TestCommentOnlyChange_GoCommentEditQualifies(t *testing.T) {
	pre := "package p\n\n// F returns one.\nfunc F() int {\n\treturn 1 // the answer\n}\n"
	post := "package p\n\n// F returns one, re-pointed at the new doc.\nfunc F() int {\n\treturn 1 // still the answer\n}\n"
	if !commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("a Go comment-only edit must qualify for the fast path")
	}
}

// Adding or removing a whole comment block, including a block comment,
// qualifies the same way.
func TestCommentOnlyChange_GoAddedBlockCommentQualifies(t *testing.T) {
	pre := "package p\n\nfunc F() int { return 1 }\n"
	post := "package p\n\n/*\nF is documented now.\n*/\nfunc F() int { return /* inline */ 1 }\n"
	if !commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("an added block comment changes no token and must qualify")
	}
}

func TestCommentOnlyChange_GoCodeChangeDoesNotQualify(t *testing.T) {
	pre := "package p\n\n// F returns one.\nfunc F() int { return 1 }\n"
	post := "package p\n\n// F returns one.\nfunc F() int { return 2 }\n"
	if commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("a changed return value is a code change")
	}
}

// A `//` inside a string literal is not a comment: the change after it on
// the same line is code, and a line-shape regex would miss it.
func TestCommentOnlyChange_GoSlashSlashInsideStringIsNotAComment(t *testing.T) {
	pre := "package p\n\nvar S = \"a // b\" + \"x\"\n"
	post := "package p\n\nvar S = \"a // b\" + \"y\"\n"
	if commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("a code change after a string's // must not qualify")
	}
}

// Build constraints are read by the go tool: flipping one changes which
// files compile.
func TestCommentOnlyChange_GoBuildConstraintIsNotAComment(t *testing.T) {
	pre := "//go:build linux\n\npackage p\n"
	post := "//go:build windows\n\npackage p\n"
	if commentOnlyChange("internal/p/p_os.go", pre, post) {
		t.Fatal("a //go:build change decides what compiles and must not qualify")
	}
}

// The legacy `// +build` line is a build constraint too, space and all.
func TestCommentOnlyChange_GoLegacyPlusBuildIsNotAComment(t *testing.T) {
	pre := "// +build linux\n\npackage p\n"
	post := "// +build windows\n\npackage p\n"
	if commentOnlyChange("internal/p/p_os.go", pre, post) {
		t.Fatal("a legacy // +build change decides what compiles and must not qualify")
	}
}

// A //go:embed directive decides what bytes ship in the binary.
func TestCommentOnlyChange_GoEmbedDirectiveIsNotAComment(t *testing.T) {
	pre := "package p\n\nimport _ \"embed\"\n\n//go:embed a.md\nvar doc string\n"
	post := "package p\n\nimport _ \"embed\"\n\n//go:embed b.md\nvar doc string\n"
	if commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("a //go:embed change swaps a compiled-in asset and must not qualify")
	}
}

// A lint directive changes what the lint stage reports, so it is judged as
// a token rather than waved through with the prose around it.
func TestCommentOnlyChange_GoNolintDirectiveIsNotAComment(t *testing.T) {
	pre := "package p\n\nfunc F() { g() }\n\nfunc g() error { return nil }\n"
	post := "package p\n\nfunc F() { g() } //nolint:errcheck\n\nfunc g() error { return nil }\n"
	if commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("an added //nolint: directive must not qualify")
	}
}

// The comment directly above `import "C"` is the cgo preamble: C source the
// toolchain compiles. Any file importing "C" sits out the fast path.
func TestCommentOnlyChange_GoCgoPreambleIsCode(t *testing.T) {
	pre := "package p\n\n// int one(void) { return 1; }\nimport \"C\"\n"
	post := "package p\n\n// int one(void) { return 2; }\nimport \"C\"\n"
	if commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("a cgo preamble edit changes compiled C and must not qualify")
	}
}

// A block comment that gains a newline right after an identifier acts as a
// newline, which inserts a semicolon: the token stream changes and the
// comparison has to see it.
func TestCommentOnlyChange_GoCommentThatInsertsASemicolonIsNotCommentOnly(t *testing.T) {
	pre := "package p\n\nvar X = a /* note */ + b\n"
	post := "package p\n\nvar X = a /* note\n*/ + b\n"
	if commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("a comment that changes semicolon insertion changes the token stream")
	}
}

// A file the scanner cannot read cleanly has no trustworthy token stream to
// compare, so the answer is the safe one.
func TestCommentOnlyChange_GoUnscannableSourceDoesNotQualify(t *testing.T) {
	pre := "package p\n\nvar S = \"unterminated\n"
	post := "package p\n\n// note\nvar S = \"unterminated\n"
	if commentOnlyChange("internal/p/p.go", pre, post) {
		t.Fatal("a source the scanner rejects must never read as comment-only")
	}
}

// The Rust path keeps its own rules through the same entry point.
func TestCommentOnlyChange_RustRoutesThroughTheRustComparison(t *testing.T) {
	if !commentOnlyChange("src/lib.rs", "// old\npub fn f() {}\n", "// new\npub fn f() {}\n") {
		t.Fatal("a Rust comment-only edit must still qualify")
	}
	fenced := "/// ```\n/// assert!(true);\n/// ```\npub fn f() {}\n"
	if commentOnlyChange("src/lib.rs", fenced, "// note\n"+fenced) {
		t.Fatal("a Rust file carrying a fenced doc example must still sit out")
	}
}

// Any other language has no comparison here, so it is never comment-only.
func TestCommentOnlyChange_OtherLanguagesNeverQualify(t *testing.T) {
	if commentOnlyChange("web/app.ts", "// a\nexport const x = 1\n", "// b\nexport const x = 1\n") {
		t.Fatal("a language without a token comparison must never read as comment-only")
	}
}

// End to end: a comment-only Go commit takes the same build-free fast path a
// comment-only Rust commit does, so no suite is ever reached.
func TestPrecommit_CommentOnlyGoCommitTakesTheFastPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "doc.go", "// Package m is documented now.\npackage m\n")
	gitDo(t, root, "add", ".")

	if res := Precommit(root, refuseToRun(t)); res.Blocked {
		t.Fatalf("a comment-only Go commit must not be blocked: %s", res.Message)
	}
	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if !strings.Contains(string(log), "comment-only-fastpath") {
		t.Fatalf("a comment-only Go commit must take the comment-only fast path, got:\n%s", log)
	}
}

// Same wiring at the merge gate.
func TestMechanical_CommentOnlyGoMergeTakesTheFastPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "doc.go", "// Package m is documented now.\npackage m\n")
	gitDo(t, root, "add", ".")

	if res := Mechanical(root, refuseToRun(t)); res.Blocked {
		t.Fatalf("a comment-only Go merge must not be blocked: %s", res.Message)
	}
	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if !strings.Contains(string(log), "comment-only-fastpath") {
		t.Fatalf("a comment-only Go merge must take the comment-only fast path, got:\n%s", log)
	}
}
