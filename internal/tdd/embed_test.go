package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// A template a Go file embeds is program behaviour, not documentation: the
// tdd skill body was wrapped mid-phrase and its own test never ran because
// the commit gate saw a docs-only .md.
func TestClassifyFile_GoEmbeddedFileIsSource(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skill.go", "package x\n\nimport _ \"embed\"\n\n//go:embed skill.md\nvar skill string\n")
	write("skill.md", "# body\n")
	write("laws.go", "package x\n\n//go:embed laws/*.toml docs/readme.md\nvar fs embed.FS\n")
	if err := os.MkdirAll(filepath.Join(dir, "laws"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("laws", "one.toml"), "a = 1\n")
	write("notes.md", "not embedded\n")

	cases := map[string]Kind{
		filepath.Join(dir, "skill.md"):          Source,
		filepath.Join(dir, "laws", "one.toml"):  Source,
		filepath.Join(dir, "notes.md"):          Ignore,
		filepath.Join(dir, "docs", "readme.md"): Source,
	}
	for p, want := range cases {
		if got := ClassifyFile(p); got != want {
			t.Errorf("ClassifyFile(%s) = %v, want %v", p, got, want)
		}
	}
}
