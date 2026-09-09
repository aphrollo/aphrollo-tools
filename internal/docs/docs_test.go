package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeRepoFile writes rel under root, creating parent dirs as needed.
func writeRepoFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// check runs CheckFiles over one doc file's content under a fresh temp repo,
// having first written every file listed in resolvable — the fixtures every
// case needs to already exist for a citation to resolve.
func check(t *testing.T, docContent string, resolvable ...string) []Finding {
	t.Helper()
	root := t.TempDir()
	for _, rel := range resolvable {
		writeRepoFile(t, root, rel, "x")
	}
	writeRepoFile(t, root, "docs/guide.md", docContent)
	got, err := CheckFiles(root, []string{"docs/guide.md"}, nil)
	if err != nil {
		t.Fatalf("CheckFiles: %v", err)
	}
	return got
}

// The extraction and resolution rule itself now lives in the ratchet engine
// (internal/ratchet's doc-path-resolves matcher, rendered from the built-in
// common/doc_reference_exists preset); these cases exercise it through the
// package's own CLI-facing entry point rather than a removed private helper.
func TestCheckFiles_ExtractsAndResolvesCitations(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		resolvable []string
		wantLines  []int // lines expected to report an UNRESOLVED reference
	}{
		{"markdown link to a resolvable relative path", "see [the plan](../plan.md)\n", []string{"plan.md"}, nil},
		{"markdown link strips the fragment before resolving", "[s](../crud.md#section-7)\n", []string{"crud.md"}, nil},
		{"markdown link strips the title before resolving", "[x](../path/to/file.go \"a title\")\n", []string{"path/to/file.go"}, nil},
		{"image link target", "![alt](../assets/diagram.png)\n", []string{"assets/diagram.png"}, nil},
		{"inline code path with extension", "edit `internal/cli/cli.go` to change it\n", []string{"internal/cli/cli.go"}, nil},
		{"multiple refs on one line, one dangling", "`a/b.go` and [c](../d/e.md) here\n", []string{"a/b.go"}, []int{1}},
		{"line numbers are tracked", "line one\n`x/y.go`\nline three\n[z](../p/q.md)\n", []string{"x/y.go"}, []int{4}},
		{"a :line suffix does not stop the file from resolving", "see `internal/ticketflow/workflow.go:288` here\n", []string{"internal/ticketflow/workflow.go"}, nil},
		{"a fake docker-tag-shaped token is never a citation", "image `apache/tika:3.3.0.0-full` pinned\n", nil, nil},
		{"a bare filename with no slash is never a citation", "run `go build` and `README.md` alone\n", nil, nil},
		{"a bare single-segment directory is never a citation", "see `node_modules/` mentioned\n", nil, nil},
		{"http link target is never a citation", "[docs](https://example.com/a/b.html)\n", nil, nil},
		{"http inline code is never a citation", "`http://example.com/x/y.go`\n", nil, nil},
		{"mailto link is never a citation", "[mail](mailto:foo@example.com)\n", nil, nil},
		{"bare anchor link is never a citation", "[top](#introduction)\n", nil, nil},
		{"absolute path inline code is never a citation", "`/usr/local/bin/aphrollo`\n", nil, nil},
		{"home path inline code is never a citation", "`~/CLAUDE.md`\n", nil, nil},
		{"an unresolved citation is reported", "see `gone/missing.md` here\n", nil, []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := check(t, tt.content, tt.resolvable...)
			var lines []int
			for _, f := range got {
				lines = append(lines, f.Line)
			}
			if !reflect.DeepEqual(lines, tt.wantLines) {
				t.Errorf("unresolved lines = %v, want %v (findings: %+v)", lines, tt.wantLines, got)
			}
		})
	}
}

// TestCheckFilesDoesNotSkipFencedCodeBlocks_knownGap pins an accepted
// behaviour change from the old hand-rolled extractor: the shared engine's
// doc-path-resolves matcher is a stateless per-line scan and cannot express
// "skip a fenced code block" the way the removed extractRefs did. A citation
// inside a fence is now judged exactly like one outside it — this is not a
// requirement, it is the shape of the matcher kind every consuming repo's own
// law is judged by too.
func TestCheckFilesDoesNotSkipFencedCodeBlocks_knownGap(t *testing.T) {
	content := "```\n`gone/inside/a/fence.go`\n```\n"
	got := check(t, content)
	if len(got) != 1 || got[0].Line != 2 {
		t.Fatalf("findings = %+v — a fenced citation is no longer exempt (accepted gap)", got)
	}
}

// TestCheckFiles_ResolvesRelativeToCitingFileThenRoot proves the resolution
// order end-to-end: a path resolves against the CITING file's own directory
// first, then the repo root, and a dangling one is reported either way.
func TestCheckFiles_ResolvesRelativeToCitingFileThenRoot(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a/sibling.md", "x")
	writeRepoFile(t, root, "rooted.md", "x")
	writeRepoFile(t, root, "a/doc.md",
		"see `./sibling.md` and `../rooted.md`\n"+
			"but `sub/nope.md` is dangling\n")

	got, err := CheckFiles(root, []string{"a/doc.md"}, nil)
	if err != nil {
		t.Fatalf("CheckFiles: %v", err)
	}
	want := []Finding{{File: "a/doc.md", Line: 2, Ref: "sub/nope.md"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckFiles() = %+v, want %+v", got, want)
	}
}

// TestCheckFiles_UsesTheRepoOwnLawWhenItDeclaresOne proves the OTHER half of
// "one implementation": a repo that declares its own doc_reference_exists law
// is judged by THAT law, not silently by the built-in default — narrowing the
// scope here to `.txt` (which the default preset never matches) is the proof.
func TestCheckFiles_UsesTheRepoOwnLawWhenItDeclaresOne(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, ".ratchet/laws/doc_reference_exists.toml", `
name = "doc_reference_exists"
description = "a repo-local override, narrowed to .txt"
severity = "deny"

[scope]
include = ["**/*.txt"]

[matcher]
kind = "doc-path-resolves"
pattern = "(?:^|[\\s(\\[`+"`"+`])((?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+\\.[A-Za-z0-9]+)"
`)
	writeRepoFile(t, root, "docs/guide.md", "see `gone/missing.md` here\n")

	got, err := CheckFiles(root, []string{"docs/guide.md"}, nil)
	if err != nil {
		t.Fatalf("CheckFiles: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a .md file must not be judged when the repo's own law scopes only .txt: %+v", got)
	}
}
