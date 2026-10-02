package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A project path with a space in it must reach events.jsonl as the real path:
// the gate.log line needs the space-free token, the event does not, and a
// token names a directory that does not exist, so repo and lane were lost.
func TestLogOverride_EventKeepsTheRealPathOfASpacedProject(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := filepath.Join(t.TempDir(), "my proj")
	for _, d := range []string{filepath.Join(repo, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		filepath.Join(".git", "HEAD"): "ref: refs/heads/lane/spaced\n",
		"go.mod":                      "module x\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	LogOverride("override-off", "sess", repo)

	data, err := os.ReadFile(filepath.Join(StateDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var e struct{ Repo, Lane string }
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	if filepath.ToSlash(e.Repo) != filepath.ToSlash(repo) || e.Lane != "lane/spaced" {
		t.Fatalf("repo/lane = %q / %q, want %q / lane/spaced", e.Repo, e.Lane, repo)
	}
}
