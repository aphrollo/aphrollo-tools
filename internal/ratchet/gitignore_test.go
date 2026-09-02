package ratchet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitignoreMatchesTheCommonPatternShapes(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitignore"), "target/\n*.log\n/out\n!keep.log\n# comment\n\nbuild/**/tmp\n")
	ig := loadGitignore(root)

	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"target", true, true},
		{"crates/pose/target", true, true},
		{"target.rs", false, false},
		{"a/b/run.log", false, true},
		{"keep.log", false, false},
		{"out", false, true},
		{"crates/out", false, false},
		{"build/x/tmp", false, true},
		{"src/main.rs", false, false},
	}
	for _, c := range cases {
		if got := ig.ignored(c.path, c.isDir); got != c.want {
			t.Errorf("ignored(%q, dir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}

func TestGitignoreAlwaysIgnoresTheGitDirAndToleratesAbsentFile(t *testing.T) {
	root := t.TempDir()
	ig := loadGitignore(root)
	if !ig.ignored(".git", true) {
		t.Error(".git must be ignored even with no .gitignore present")
	}
	if ig.ignored("src/main.rs", false) {
		t.Error("a repo with no .gitignore must ignore nothing else")
	}
}

func TestGitignoreReadsNestedFilesRelativeToTheirOwnDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "crates", "pose"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "crates", "pose", ".gitignore"), "generated.rs\n")
	ig := loadGitignore(root)
	if !ig.ignored("crates/pose/generated.rs", false) {
		t.Error("a nested .gitignore must apply below its own directory")
	}
	if ig.ignored("crates/other/generated.rs", false) {
		t.Error("a nested .gitignore must not apply outside its own directory")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
