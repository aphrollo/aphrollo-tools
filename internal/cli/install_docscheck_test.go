package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/docs"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Issue #888: every file install writes into a repo is committed there and
// judged by that repo's docs check, and the shipped templates cited paths
// that exist only in aphrollo's own tree. A consuming repo that turned the
// check on started red on files it did not author. This installs every
// template, project-scoped, into a repo holding nothing else, commits the
// result, and runs the docs check's own resolver over it.
func TestInstall_EveryShippedTemplatePassesTheDocsCheckInABareRepo(t *testing.T) {
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := resolvedTempDir(t)
	gitInitRepo(t, repo)
	gateConfigDir(t)

	args := []string{"install", "--repo", repo, "--bin", fakeInstalledBin(t),
		"--config-dir", filepath.Join(repo, ".claude"), "--no-git", "--claude-md", "--ratchet-readme"}
	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	add := fixtureGit("add", "-A")
	add.Dir = repo
	if b, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, b)
	}

	files, err := docs.TrackedMarkdown(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CLAUDE.md",
		".ratchet/README.md",
		".claude/skills/tdd/SKILL.md",
		".claude/skills/sdd/SKILL.md",
		".claude/agents/builder.md",
		".claude/agents/researcher.md",
		".claude/agents/reviewer.md",
	} {
		if !slices.Contains(files, want) {
			t.Errorf("install wrote no %s, so the check below never judged it (have %v)", want, files)
		}
	}

	var findings bytes.Buffer
	missed, err := docs.Check(repo, nil, &findings)
	if err != nil {
		t.Fatal(err)
	}
	if missed {
		t.Errorf("the templates install writes cite paths a consuming repo does not have:\n%s", findings.String())
	}
}
