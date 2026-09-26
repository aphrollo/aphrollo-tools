package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "work-kanban", want: "work-kanban"},
		{in: "feat/foo", want: "feat-foo"},
		{in: "a/b/c", want: "a-b-c"},
		{in: "v1.2_x", want: "v1.2_x"},
		{in: "", wantErr: true},
		{in: "../escape", wantErr: true},
		{in: "/abs", wantErr: true},
		{in: "trailing/", wantErr: true},
		{in: "has space", wantErr: true},
		{in: "semi;rm", wantErr: true},
		// A leading dash would be parsed as a FLAG, not a positional, by every
		// git/gh sink the slug flows into (`git push -u origin <slug>`,
		// `gh pr merge <slug>`, `git worktree add … <slug>`). Reject it so a
		// branch like "-D", "--force", or "--delete/x" can't inject an option.
		{in: "-D", wantErr: true},
		{in: "--force", wantErr: true},
		{in: "--delete/x", wantErr: true},
		{in: "-leadingdash", wantErr: true},
	}
	for _, c := range cases {
		got, err := Slugify(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("Slugify(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Slugify(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDefaultWorktreeBase(t *testing.T) {
	got := DefaultWorktreeBase("/home/debian/spaces/aphrollo/aphrollo-web")
	want := filepath.FromSlash("/home/debian/spaces/aphrollo/.worktrees/aphrollo-web")
	if got != want {
		t.Fatalf("DefaultWorktreeBase = %q, want %q", got, want)
	}
}

func TestRender_DryRun(t *testing.T) {
	p := &Plan{
		Repo: "/r", RepoName: "r", Branch: "feat/x", Slug: "feat-x",
		Worktree: "/r/.worktrees/feat-x", BranchExists: false,
		Steps: []Step{
			{Title: "mark git-safe", Cmd: []string{"git", "config", "--global", "--add", "safe.directory", "/r"}, Skip: "already a safe.directory"},
			{Title: "create worktree", Cmd: []string{"git", "-C", "/r", "worktree", "add", "-b", "feat/x", "/r/.worktrees/feat-x"}},
			{Title: "install dependencies", Cmd: []string{"pnpm", "install"}, Dir: "/r/.worktrees/feat-x"},
		},
	}
	out := Render(p, false)
	for _, want := range []string{
		"new branch", "/r/.worktrees/feat-x",
		"1. [skip]", "already a safe.directory",
		"2. [run]", "worktree add",
		"3. [run] pnpm install", "(cwd /r/.worktrees/feat-x)",
		"--dry",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Render output missing %q:\n%s", want, out)
		}
	}
}

func TestShellJoin_QuotesSpaces(t *testing.T) {
	got := shellJoin([]string{"git", "commit", "-m", "a b"})
	if !strings.Contains(got, "'a b'") {
		t.Fatalf("shellJoin should quote spaced arg: %q", got)
	}
}
