package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rustDiffRepo commits a single Rust file at before, then stages it at after
// -- the minimal shape commentOnlyRustFile needs to diff HEAD against the
// index. Not a real crate (no Cargo.toml): the decision functions this file
// tests only ever read git blobs, never invoke cargo.
func rustDiffRepo(t *testing.T, before, after string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "src/lib.rs", before)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "src/lib.rs", after)
	gitDo(t, root, "add", ".")
	return root
}

// A `//` comment-only edit changes no token outside a comment, so it
// qualifies for the fast path -- the exact shape issue #723 reported (a
// re-pointed doc/line comment triggering a full clippy/check/suite).
func TestCommentOnlyRustFile_PlainCommentChangeQualifies(t *testing.T) {
	root := rustDiffRepo(t,
		"// old note\npub fn f() -> i32 { 1 }\n",
		"// new note, re-pointed\npub fn f() -> i32 { 1 }\n")
	if !commentOnlyRustFile(root, "src/lib.rs") {
		t.Fatal("a `//` comment-only edit must qualify for the fast path")
	}
}

// Decision: a doc comment carrying no fenced example has no doctest to
// reshape, so it qualifies exactly like a plain `//` comment.
func TestCommentOnlyRustFile_DocCommentWithoutFenceQualifies(t *testing.T) {
	root := rustDiffRepo(t,
		"/// Old summary.\npub fn f() -> i32 { 1 }\n",
		"/// New summary, re-pointed.\npub fn f() -> i32 { 1 }\n")
	if !commentOnlyRustFile(root, "src/lib.rs") {
		t.Fatal("a fence-free doc comment edit must qualify for the fast path")
	}
}

// A fenced doc comment feeds rustdoc a doctest: changing text inside that
// block can change what compiles and what runs, so a file carrying one never
// takes the fast path -- token-identical outside the comment or not.
func TestCommentOnlyRustFile_FencedDocCommentNeverQualifies(t *testing.T) {
	root := rustDiffRepo(t,
		"/// ```\n/// assert_eq!(f(), 1);\n/// ```\npub fn f() -> i32 { 1 }\n",
		"/// ```\n/// assert_eq!(f(), 2 - 1);\n/// ```\npub fn f() -> i32 { 1 }\n")
	if commentOnlyRustFile(root, "src/lib.rs") {
		t.Fatal("a doc comment carrying a fenced example must never take the fast path")
	}
}

// `#[doc = "..."]` is an attribute, not a comment: it participates in
// compilation, so changing its string content is a real token change.
func TestCommentOnlyRustFile_DocAttributeIsNotAComment(t *testing.T) {
	root := rustDiffRepo(t,
		"#[doc = \"Old summary.\"]\npub fn f() -> i32 { 1 }\n",
		"#[doc = \"New summary.\"]\npub fn f() -> i32 { 1 }\n")
	if commentOnlyRustFile(root, "src/lib.rs") {
		t.Fatal("#[doc = \"...\"] is an attribute, not a comment -- must not qualify")
	}
}

// A `//` inside a string literal does not open a comment: the masker tracks
// quote state, so a real code change sitting after one in the same line must
// still be seen as a token change, not hidden behind a false comment-opener.
func TestCommentOnlyRustFile_SlashSlashInsideStringIsNotACommentOpener(t *testing.T) {
	root := rustDiffRepo(t,
		"pub fn f() -> i32 { let _s = \"a // b\"; 1 }\n",
		"pub fn f() -> i32 { let _s = \"a // b\"; 2 }\n")
	if commentOnlyRustFile(root, "src/lib.rs") {
		t.Fatal("a real code change after a string's // must not qualify")
	}
}

// One file with a real code change alongside a comment-only one takes the
// whole commit off the fast path.
func TestCommentOnlyRust_MixedDiffDoesNotQualify(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "src/a.rs", "// old\npub fn a() -> i32 { 1 }\n")
	write(t, root, "src/b.rs", "pub fn b() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	write(t, root, "src/a.rs", "// new, re-pointed\npub fn a() -> i32 { 1 }\n")
	write(t, root, "src/b.rs", "pub fn b() -> i32 { 2 }\n")
	gitDo(t, root, "add", ".")

	if commentOnlySource(root) {
		t.Fatal("one real code change among staged files must take the whole commit off the fast path")
	}
}

// End to end: a comment-only Rust commit must reach no stage that could take
// the machine-wide build slot, exactly like a docs-only one.
func TestPrecommit_CommentOnlyRustCommitRunsNoStageThatCouldQueue(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "// re-pointed note\npub fn base() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")

	if res := Precommit(root, refuseToRun(t)); res.Blocked {
		t.Fatalf("a comment-only Rust commit must not be blocked: %s", res.Message)
	}
	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if !strings.Contains(string(log), "comment-only-fastpath") {
		t.Fatalf("the fast path must name itself in the log, got:\n%s", log)
	}
}

// Same wiring at the merge gate.
func TestMechanical_CommentOnlyRustMergeTakesTheFastPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "// re-pointed note\npub fn base() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")

	if res := Mechanical(root, refuseToRun(t)); res.Blocked {
		t.Fatalf("a comment-only Rust merge must not be blocked: %s", res.Message)
	}
	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if !strings.Contains(string(log), "comment-only-fastpath") {
		t.Fatalf("the fast path must name itself in the log, got:\n%s", log)
	}
}

// A comment carries a directive too: an added lint suppression must still
// block at commit even though the whole diff is otherwise comment-only, so
// the anti-cheat scan cannot be skipped just because the build and suite are.
func TestPrecommit_CommentOnlyRustStillRunsTheSuppressionScan(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "src/lib.rs", "// eslint-disable pending review\npub fn base() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, refuseToRun(t))
	if !res.Blocked || !strings.Contains(res.Message, suppressionCommitHeader) {
		t.Fatalf("a comment-only edit that adds a suppression must still block: %+v", res)
	}
}

// Same for the declared laws: a directive a law reads must still be judged
// even when the diff never leaves a comment.
func TestPrecommit_CommentOnlyRustStillRunsTheRatchetLaws(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-todo-rs.toml"), `
name = "no-todo-rs"
description = "Rust comments do not ship TODOs"
severity = "deny"
escape = "todo-ok:"
baseline = ".ratchet/baselines/no-todo-rs.txt"

[scope]
include = ["**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "no-todo-rs.txt"), "")
	mustWrite(t, filepath.Join(root, "src", "lib.rs"), "// clean\npub fn base() -> i32 { 0 }\n")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "src", "lib.rs"), "// TODO: revisit\npub fn base() -> i32 { 0 }\n")
	gitAddAll(t, root)

	res := Precommit(root, refuseToRun(t))
	if !res.Blocked || !strings.Contains(res.Message, "no-todo-rs") {
		t.Fatalf("a comment-only Rust edit must still answer to the laws: %+v", res)
	}
}
