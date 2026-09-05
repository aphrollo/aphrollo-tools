package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// --- discardIntent: pure classification, no process spawning -----------

// TestDiscardIntent_ClassifiesEachForm pins the exact (form, paths, ok) the
// discard wall's classifier produces for every discarding shape the spec
// names, and the near-miss spellings (a bare `reset`, a plain `checkout
// <branch>`, `restore --staged` alone, a dry-run `clean`) that must NOT
// classify as discards -- those are either read-only, reversible, or (a
// branch move) another wall's job.
func TestDiscardIntent_ClassifiesEachForm(t *testing.T) {
	cases := []struct {
		name      string
		rest      []string
		wantForm  string
		wantPaths []string
		wantOK    bool
	}{
		{"reset --hard", []string{"reset", "--hard"}, "reset --hard", nil, true},
		{"reset --soft is not a discard", []string{"reset", "--soft", "HEAD~1"}, "", nil, false},
		{"checkout -- paths", []string{"checkout", "--", "a.txt", "b.txt"}, "checkout -- <paths>", []string{"a.txt", "b.txt"}, true},
		{"checkout dot", []string{"checkout", "."}, "checkout -- <paths>", []string{"."}, true},
		{"checkout branch is not a discard", []string{"checkout", "main"}, "", nil, false},
		{"checkout -f branch", []string{"checkout", "-f", "main"}, "checkout -f", nil, true},
		{"restore path", []string{"restore", "b.txt"}, "restore <paths>", []string{"b.txt"}, true},
		{"restore --staged alone is not a discard", []string{"restore", "--staged", "b.txt"}, "", nil, false},
		{"restore -S alone is not a discard", []string{"restore", "-S", "b.txt"}, "", nil, false},
		{"restore -SW bundled", []string{"restore", "-SW", "b.txt"}, "restore <paths>", []string{"b.txt"}, true},
		{"restore --source= doesn't change classification", []string{"restore", "--source=HEAD~1", "b.txt"}, "restore <paths>", []string{"b.txt"}, true},
		{"restore -- ends options", []string{"restore", "--", "-x"}, "restore <paths>", []string{"-x"}, true},
		{"clean -fd", []string{"clean", "-fd"}, "clean -fd", nil, true},
		{"clean -n is a dry run", []string{"clean", "-n"}, "", nil, false},
		{"clean -fdn is still a dry run", []string{"clean", "-fdn"}, "", nil, false},
		{"clean -- ends options", []string{"clean", "-fd", "--", "-weird"}, "clean -fd", []string{"-weird"}, true},
		{"stash drop", []string{"stash", "drop"}, "stash drop", nil, true},
		{"stash push is reversible", []string{"stash", "push"}, "", nil, false},
		{"branch -D", []string{"branch", "-D", "x"}, "branch -D x", nil, true},
		{"branch -d is not forced", []string{"branch", "-d", "x"}, "", nil, false},
		{"branch -fD bundled", []string{"branch", "-fD", "x"}, "branch -D x", nil, true},
		{"branch -Df bundled", []string{"branch", "-Df", "x"}, "branch -D x", nil, true},
		{"worktree remove --force", []string{"worktree", "remove", "--force", "/w"}, "worktree remove --force", []string{"/w"}, true},
		{"worktree remove -f short form", []string{"worktree", "remove", "-f", "/w"}, "worktree remove --force", []string{"/w"}, true},
		{"worktree remove without force", []string{"worktree", "remove", "/w"}, "", nil, false},
		{"status is read-only", []string{"status"}, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotForm, gotPaths, gotOK := discardIntent(c.rest)
			if gotOK != c.wantOK {
				t.Fatalf("discardIntent(%v) ok = %v, want %v", c.rest, gotOK, c.wantOK)
			}
			if !gotOK {
				return
			}
			if gotForm != c.wantForm {
				t.Fatalf("discardIntent(%v) form = %q, want %q", c.rest, gotForm, c.wantForm)
			}
			if !equalStrings(gotPaths, c.wantPaths) {
				t.Fatalf("discardIntent(%v) paths = %v, want %v", c.rest, gotPaths, c.wantPaths)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- discardCostOf: real git against real fixture repos -----------------
//
// Same reasoning as git_shim_mergeabort_test.go's recoverRejectedMerge
// tests: this inspects actual git plumbing (diff shortstat numbers,
// ls-files, rev-list, stash list), which a synthetic argv-echoing stub
// cannot produce truthfully.

// newDiscardFixture builds a one-commit repo (branch main, one tracked
// file) using the REAL git binary, resolved past the queue shim exactly as
// realGitForTest does elsewhere in this package.
func newDiscardFixture(t *testing.T) (repo, realGit string) {
	t.Helper()
	isolateGitConfigCLI(t)
	realGit = realGitForTest(t)
	repo = t.TempDir()
	runFixtureGit(t, realGit, repo, "init", "-q", "-b", "main")
	runFixtureGit(t, realGit, repo, "config", "user.email", "t@t")
	runFixtureGit(t, realGit, repo, "config", "user.name", "t")
	writeFixtureFile(t, repo, "seed.txt", []string{"seed"})
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "seed")
	return repo, realGit
}

func runFixtureGit(t *testing.T, realGit, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command(realGit, append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// writeFixtureFile writes lines (each becomes its own newline-terminated
// line) to name inside repo.
func writeFixtureFile(t *testing.T, repo, name string, lines []string) {
	t.Helper()
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// distinctLines generates n lines that share no content with any other
// call's lines (a unique tag per call plus the index), so a diff between an
// "old" and a "new" generation has NO common subsequence: git's diff
// algorithm then reports exactly len(old) deletions and len(new)
// insertions for the file, with nothing left for line-matching to shrink.
func distinctLines(tag string, n int) []string {
	lines := make([]string, n)
	for i := 0; i < n; i++ {
		lines[i] = fmt.Sprintf("%s-%d", tag, i)
	}
	return lines
}

func TestDiscardCost_CountsModifiedFilesAndDiffNumbers(t *testing.T) {
	repo, realGit := newDiscardFixture(t)
	// Three tracked files rewritten with wholly distinct content: deletions
	// 20+10+10=40, insertions 100+80+32=212, three files changed.
	writeFixtureFile(t, repo, "a.txt", distinctLines("a-orig", 20))
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-orig", 10))
	writeFixtureFile(t, repo, "c.txt", distinctLines("c-orig", 10))
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "tracked base")

	writeFixtureFile(t, repo, "a.txt", distinctLines("a-new", 100))
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-new", 80))
	writeFixtureFile(t, repo, "c.txt", distinctLines("c-new", 32))
	writeFixtureFile(t, repo, "u1.txt", []string{"untracked"})
	writeFixtureFile(t, repo, "u2.txt", []string{"untracked"})

	c := discardCostOf(realGit, repo, "reset --hard", nil)
	if c.Files != 3 || c.Insertions != 212 || c.Deletions != 40 {
		t.Fatalf("discardCostOf: Files=%d Insertions=%d Deletions=%d, want 3/212/40", c.Files, c.Insertions, c.Deletions)
	}
	if c.Untracked != 2 {
		t.Fatalf("discardCostOf: Untracked=%d, want 2", c.Untracked)
	}
}

func TestDiscardCost_RestrictsToTheNamedPaths(t *testing.T) {
	repo, realGit := newDiscardFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("a-orig", 1))
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-orig", 2))
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "tracked base")

	writeFixtureFile(t, repo, "a.txt", distinctLines("a-new", 5)) // +5/-1
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-new", 7)) // +7/-2

	c := discardCostOf(realGit, repo, "restore <paths>", []string{"b.txt"})
	if c.Files != 1 || c.Insertions != 7 || c.Deletions != 2 {
		t.Fatalf("discardCostOf(restore, [b.txt]): Files=%d Insertions=%d Deletions=%d, want 1/7/2", c.Files, c.Insertions, c.Deletions)
	}
}

// TestDiscardCost_RestoreCountsOnlyUnstagedChanges pins that `restore
// <paths>` (and, by the same code path, `checkout -- <paths>`) measure the
// worktree against the INDEX, not HEAD: those forms only overwrite the
// worktree from what is already staged, so staged-but-uncommitted work is
// not part of what they would destroy. `reset --hard`, by contrast, still
// measures against HEAD -- it discards staged work too.
func TestDiscardCost_RestoreCountsOnlyUnstagedChanges(t *testing.T) {
	repo, realGit := newDiscardFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("staged", 2))
	runFixtureGit(t, realGit, repo, "add", "a.txt")
	writeFixtureFile(t, repo, "a.txt", append(distinctLines("staged", 2), distinctLines("unstaged", 1)...))

	c := discardCostOf(realGit, repo, "restore <paths>", []string{"a.txt"})
	if c.Files != 1 || c.Insertions != 1 {
		t.Fatalf("discardCostOf(restore, [a.txt]) with a staged and an unstaged edit: Files=%d Insertions=%d, want 1/1", c.Files, c.Insertions)
	}

	runFixtureGit(t, realGit, repo, "add", "a.txt")
	fullyStaged := discardCostOf(realGit, repo, "restore <paths>", []string{"a.txt"})
	if !fullyStaged.zero() {
		t.Fatalf("discardCostOf(restore, [a.txt]) with everything staged = %+v, want zero()", fullyStaged)
	}

	reset := discardCostOf(realGit, repo, "reset --hard", nil)
	if reset.Insertions != 3 {
		t.Fatalf("discardCostOf(reset --hard) over the same tree: Insertions=%d, want 3", reset.Insertions)
	}
}

func TestDiscardCost_IsZeroOnACleanTree(t *testing.T) {
	repo, realGit := newDiscardFixture(t)
	c := discardCostOf(realGit, repo, "reset --hard", nil)
	if !c.zero() {
		t.Fatalf("discardCostOf on a clean tree = %+v, want zero()", c)
	}
}

// TestDiscardCost_WorktreeCostResolvesARelativePathAgainstWorkDir pins that
// `worktree remove --force <path>` measures inside <path> even when <path>
// is relative -- git itself accepts a relative worktree path, and a relative
// cmd.Dir resolves against the CALLING process's cwd, not workDir, so the
// measurement has to join it onto workDir itself first.
func TestDiscardCost_WorktreeCostResolvesARelativePathAgainstWorkDir(t *testing.T) {
	isolateGitConfigCLI(t)
	realGit := realGitForTest(t)
	root := t.TempDir()
	repoDir := filepath.Join(root, "wt")
	if err := os.Mkdir(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, realGit, repoDir, "init", "-q", "-b", "main")
	runFixtureGit(t, realGit, repoDir, "config", "user.email", "t@t")
	runFixtureGit(t, realGit, repoDir, "config", "user.name", "t")
	writeFixtureFile(t, repoDir, "seed.txt", []string{"seed"})
	runFixtureGit(t, realGit, repoDir, "add", ".")
	runFixtureGit(t, realGit, repoDir, "commit", "-qm", "seed")
	writeFixtureFile(t, repoDir, "seed.txt", distinctLines("seed-new", 3))

	c := discardCostOf(realGit, root, "worktree remove --force", []string{"wt"})
	if c.Files != 1 || c.Insertions != 3 || c.Deletions != 1 {
		t.Fatalf("discardCostOf(worktree remove, relative path) from workDir %s: Files=%d Insertions=%d Deletions=%d, want 1/3/1", root, c.Files, c.Insertions, c.Deletions)
	}
	if c.Worktree != repoDir {
		t.Fatalf("discardCostOf(worktree remove, relative path): Worktree = %q, want %q", c.Worktree, repoDir)
	}
}

func TestDiscardCost_CountsUnmergedCommitsForBranchDelete(t *testing.T) {
	repo, realGit := newDiscardFixture(t)
	runFixtureGit(t, realGit, repo, "branch", "x")
	runFixtureGit(t, realGit, repo, "checkout", "-q", "x")
	writeFixtureFile(t, repo, "x1.txt", []string{"one"})
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "x commit 1")
	writeFixtureFile(t, repo, "x2.txt", []string{"two"})
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "x commit 2")
	runFixtureGit(t, realGit, repo, "checkout", "-q", "main")
	runFixtureGit(t, realGit, repo, "branch", "y") // at HEAD, nothing ahead

	cx := discardCostOf(realGit, repo, "branch -D x", nil)
	if cx.UnmergedCommits != 2 {
		t.Fatalf("discardCostOf(branch -D x): UnmergedCommits = %d, want 2", cx.UnmergedCommits)
	}
	cy := discardCostOf(realGit, repo, "branch -D y", nil)
	if cy.UnmergedCommits != 0 {
		t.Fatalf("discardCostOf(branch -D y): UnmergedCommits = %d, want 0", cy.UnmergedCommits)
	}
}

func TestDiscardCost_CountsStashEntries(t *testing.T) {
	repo, realGit := newDiscardFixture(t)
	writeFixtureFile(t, repo, "seed.txt", []string{"seed", "changed"})
	runFixtureGit(t, realGit, repo, "stash", "push", "-qm", "wip")

	c := discardCostOf(realGit, repo, "stash drop", nil)
	if c.Stashes != 1 {
		t.Fatalf("discardCostOf(stash drop): Stashes = %d, want 1", c.Stashes)
	}
}

// --- discardRefusalLine: pure rendering, no process spawning ------------

// TestDiscardRefusalLine_RendersEachShape pins the exact bytes the wall
// prints for each form's shape, including that untracked count rides along
// on discardCost but is never printed for reset (git's reset --hard leaves
// untracked files alone -- nothing was actually destroyed there).
// TestDiscardCost_ReportsAMeasurementFailureInsteadOfZero pins fail-closed:
// a git that cannot even run (never mind measure) must not read as "nothing
// to discard" -- that would let the wall wave the command through on its
// own failure. Err carries the failure; zero() is false; the refusal line
// names the error and the retry instead of a (bogus) file/diff count.
func TestDiscardCost_ReportsAMeasurementFailureInsteadOfZero(t *testing.T) {
	repo := t.TempDir()
	brokenGit := filepath.Join(t.TempDir(), "no-such-git-binary")

	c := discardCostOf(brokenGit, repo, "reset --hard", nil)
	if c.Err == nil {
		t.Fatalf("discardCostOf with a broken git binary: Err = nil, want non-nil")
	}
	if c.zero() {
		t.Fatalf("discardCostOf with a broken git binary: zero() = true, want false (Err = %v)", c.Err)
	}

	line := discardRefusalLine("reset --hard", c)
	wantPrefix := "gate: refused — reset --hard: could not measure what it would discard ("
	wantSuffix := "); retry, or APHROLLO_DISCARD=1 to bypass"
	if !strings.HasPrefix(line, wantPrefix) || !strings.HasSuffix(line, wantSuffix) {
		t.Fatalf("discardRefusalLine on a measurement failure =\n%q\nwant prefix %q and suffix %q", line, wantPrefix, wantSuffix)
	}
}

func TestDiscardRefusalLine_RendersEachShape(t *testing.T) {
	const tail = "; aphrollo gate allow discard arms one command, APHROLLO_DISCARD=1 for scripts"
	cases := []struct {
		name string
		form string
		cost discardCost
		want string
	}{
		{
			"reset --hard",
			"reset --hard",
			discardCost{Files: 3, Insertions: 212, Deletions: 40, Untracked: 2},
			"gate: refused — reset --hard discards 3 file(s), +212/-40 uncommitted" + tail,
		},
		{
			"clean -fd",
			"clean -fd",
			discardCost{Untracked: 2},
			"gate: refused — clean -fd discards 2 untracked file(s)" + tail,
		},
		{
			"stash drop",
			"stash drop",
			discardCost{Stashes: 1},
			"gate: refused — stash drop discards 1 stash entry(ies)" + tail,
		},
		{
			"branch -D x",
			"branch -D x",
			discardCost{UnmergedCommits: 2},
			"gate: refused — branch -D x discards 2 unmerged commit(s)" + tail,
		},
		{
			"worktree remove --force",
			"worktree remove --force",
			discardCost{Files: 1, Insertions: 3, Deletions: 1, Worktree: "/w"},
			"gate: refused — worktree remove --force discards 1 file(s), +3/-1 uncommitted in /w" + tail,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := discardRefusalLine(c.form, c.cost); got != c.want {
				t.Fatalf("discardRefusalLine(%q, %+v) =\n%q\nwant\n%q", c.form, c.cost, got, c.want)
			}
		})
	}
}
