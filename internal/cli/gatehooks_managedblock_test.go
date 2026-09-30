package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// managedBlockCommit builds a repo whose committed CLAUDE.md carries the block
// this build renders, stages an aphrollo.toml that changes what the block
// should say, and leaves the shell in it.
func managedBlockCommit(t *testing.T, refreshBlock bool) {
	t.Helper()
	gateConfigDir(t)
	isolateGit(t)
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := fixtureGit(append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	put := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	put("CLAUDE.md", "# repo\n\n"+tdd.ClaudeMDBlock(tdd.BlockFlags{}))
	run("add", ".")
	run("commit", "-qm", "base")
	put("aphrollo.toml", "[aphrollo]\nundercover = true\n")
	run("add", "aphrollo.toml")
	if refreshBlock {
		put("CLAUDE.md", "# repo\n\n"+tdd.ClaudeMDBlock(tdd.BlockFlags{Undercover: true}))
		run("add", "CLAUDE.md")
	}
	t.Chdir(repo)
}

// An aphrollo.toml-only commit touches no Go file and no CLAUDE.md, so no
// other stage compares the block with what the declarations now render.
func TestGatePrecommit_RefusesAnAphrolloTomlChangeThatLeavesTheManagedBlockStale(t *testing.T) {
	managedBlockCommit(t, false)

	var errb bytes.Buffer
	if code := Run([]string{"gate", "precommit"}, strings.NewReader(""), &bytes.Buffer{}, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "aphrollo install --managed-block-only --repo .") {
		t.Fatalf("stderr = %q, want the one command that fixes it", errb.String())
	}
}

func TestGatePrecommit_AdmitsTheAphrolloTomlChangeThatCarriesTheRefreshedBlock(t *testing.T) {
	managedBlockCommit(t, true)

	var errb bytes.Buffer
	if code := Run([]string{"gate", "precommit"}, strings.NewReader(""), &bytes.Buffer{}, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, errb.String())
	}
}
