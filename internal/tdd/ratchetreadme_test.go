package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRatchetReadmeIsByteIdenticalOnASecondRun(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ratchet", "laws"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".ratchet", "README.md")

	changed, err := WriteRatchetReadme(repo, false)
	if err != nil || !changed {
		t.Fatalf("first write: changed = %v, err = %v", changed, err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the managed file: %v", err)
	}
	if !strings.Contains(string(first), "matcher kinds") && !strings.Contains(string(first), "Matcher kinds") {
		t.Errorf("the spec must carry the schema it is the spec for:\n%s", first[:200])
	}

	changed, err = WriteRatchetReadme(repo, false)
	if err != nil || changed {
		t.Fatalf("second write: changed = %v, err = %v — a repeat run rewrites nothing", changed, err)
	}
	second, _ := os.ReadFile(path)
	if string(second) != string(first) {
		t.Error("a second run must be byte-identical")
	}

	// A hand edit is reverted: the file is owned by the tool, and a local fix
	// that survives is a fork of the spec nobody else gets.
	if err := os.WriteFile(path, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := WriteRatchetReadme(repo, false); err != nil || !changed {
		t.Fatalf("an edited file must be rewritten: changed = %v, err = %v", changed, err)
	}
}

func TestWriteRatchetReadmeSkipsARepoWithNoRatchetDir(t *testing.T) {
	repo := t.TempDir()
	if changed, err := WriteRatchetReadme(repo, false); err != nil || changed {
		t.Fatalf("changed = %v, err = %v — a repo with no laws gets no file", changed, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".ratchet")); !os.IsNotExist(err) {
		t.Error("init must not create a .ratchet dir in a repo that has none")
	}
	if changed, err := WriteRatchetReadme(repo, true); err != nil || !changed {
		t.Fatalf("--ratchet-readme writes it anyway: changed = %v, err = %v", changed, err)
	}
}

// This repo's own .ratchet/README.md is written by the SAME generator every
// consuming repo gets. A hand edit landing here instead of in the template
// survives review and CI, then is discarded without warning the next time
// `gate init`/`update` regenerates the file (issue #517: two merged PRs'
// documentation ended up in this generated file instead of the template and
// were reverted). This test is the guard: it goes red the moment the tracked
// file and the template diverge.
func TestOwnRatchetReadme_MatchesTheGeneratedOutput(t *testing.T) {
	path := filepath.Join(repoRootForTest(t), ".ratchet", "README.md")
	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	want := RatchetReadme()
	if string(have) != want {
		t.Errorf("%s has drifted from RatchetReadme()'s output — port the content into internal/tdd/ratchet_laws.md, never hand-edit the generated file", path)
	}
}

// The spec has ONE source: aphrollo's README carries the same text between its
// ratchet-spec markers, so a schema change cannot land in one and not the other.
func TestRatchetSpecIsTheSameTextAsTheReadmeSection(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	readme := strings.ReplaceAll(string(data), "\r\n", "\n")
	const begin, end = "<!-- ratchet-spec:begin -->\n", "<!-- ratchet-spec:end -->"
	i := strings.Index(readme, begin)
	j := strings.Index(readme, end)
	if i < 0 || j < 0 {
		t.Fatal("README.md lost its ratchet-spec markers — the managed file has no source")
	}
	section := strings.TrimSpace(readme[i+len(begin) : j])
	spec := strings.TrimSpace(strings.ReplaceAll(ratchetLawsSpec, "\r\n", "\n"))
	if section != spec {
		t.Error("internal/tdd/ratchet_laws.md and the README's ratchet-spec section have drifted")
	}
}
