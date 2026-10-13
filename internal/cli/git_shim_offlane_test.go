package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// offLaneShimFixture is a repo on lane/x with uncommitted work in a tracked file and an
// aphrollo.toml declaring the given off share.
func offLaneShimFixture(t *testing.T, share string) (repo string, cfg gitShimConfig) {
	t.Helper()
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateConfigDir(t)
	repo, cfg = discardWallFixture(t)
	runFixtureGit(t, cfg.realGit, repo, "checkout", "-q", "-b", "lane/x")
	writeFixtureFile(t, repo, "a.txt", []string{"orig"})
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "tracked base")
	writeFixtureFile(t, repo, "a.txt", distinctLines("new", 30))
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte("[aphrollo]\nab-off-share = "+share+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, cfg
}

// The discard wall is a wall of aphrollo's: an off lane runs git as plain git.
func TestGitShim_AnOffLaneRunsResetHardWithoutTheDiscardWall(t *testing.T) {
	repo, cfg := offLaneShimFixture(t, "100")
	var out, errb bytes.Buffer
	if code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0 for an off lane\nstderr: %s", code, errb.String())
	}
	body, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "new-0") {
		t.Fatal("reset --hard did not run: a.txt still carries the edit")
	}
}

// The control: the same lane with no off share is refused by the wall.
func TestGitShim_ALaneWithNoOffShareStillMeetsTheDiscardWall(t *testing.T) {
	_, cfg := offLaneShimFixture(t, "0")
	var out, errb bytes.Buffer
	if code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg); code != 1 {
		t.Fatalf("exit = %d, want 1 (the discard wall)\nstderr: %s", code, errb.String())
	}
}
