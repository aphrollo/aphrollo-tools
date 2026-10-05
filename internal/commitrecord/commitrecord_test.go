package commitrecord

import (
	"os/exec"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repoWithState(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	run(t, dir, "config", "user.email", "o@example.com")
	run(t, dir, "config", "user.name", "o")
	run(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func TestRecord_KeepsEveryCommitTheHookSawAndOnlyThose(t *testing.T) {
	dir := repoWithState(t)
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	first := run(t, dir, "rev-parse", "HEAD")
	Record(dir)
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	second := run(t, dir, "rev-parse", "HEAD")
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "three, no hook")
	third := run(t, dir, "rev-parse", "HEAD")
	run(t, dir, "reset", "-q", "--hard", second)
	Record(dir)

	got := Recorded(dir)
	if !got[first] || !got[second] || got[third] {
		t.Fatalf("recorded = %v, want first and second only (first %s, second %s, third %s)", got, first, second, third)
	}
}

func TestRecord_IsSharedByEveryWorktreeOfOneRepository(t *testing.T) {
	dir := repoWithState(t)
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	lane := t.TempDir() + "/lane"
	run(t, dir, "worktree", "add", "-q", "-b", "lane", lane)
	run(t, lane, "commit", "-q", "--allow-empty", "-m", "in lane")
	Record(lane)

	if !Recorded(dir)[run(t, lane, "rev-parse", "HEAD")] {
		t.Fatal("a commit recorded from a lane is not visible from the primary checkout")
	}
}

func TestRecord_OutsideARepositoryOrWithoutStateFailsOpen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	Record(t.TempDir())
	if got := Recorded(t.TempDir()); len(got) != 0 {
		t.Fatalf("recorded = %v, want none outside a repository", got)
	}
}

func TestRecordSHAs_KeepsExactlyTheShasGivenAcrossCalls(t *testing.T) {
	dir := repoWithState(t)
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	head := run(t, dir, "rev-parse", "HEAD")
	other := strings.Repeat("c", 40)

	RecordSHAs(dir, nil)
	if got := Recorded(dir); len(got) != 0 {
		t.Fatalf("recorded %v for no shas, want nothing", got)
	}
	RecordSHAs(dir, []string{head})
	RecordSHAs(dir, []string{other, strings.Repeat("d", 40)})

	got := Recorded(dir)
	if len(got) != 3 || !got[head] || !got[other] {
		t.Errorf("recorded = %v, want the three shas given in two calls", got)
	}
	RecordSHAs(t.TempDir(), []string{"eee"})
	if Recorded(dir)["eee"] {
		t.Error("a sha given for a directory outside any repository reached this repository's record")
	}
}

func TestRecordedSince_IsWhatWasRecordedAfterTheMark(t *testing.T) {
	dir := repoWithState(t)
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	a, b, c := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	if got := Mark(dir); got != 0 {
		t.Fatalf("mark of an empty record = %d, want 0", got)
	}
	RecordSHAs(dir, []string{a, b})
	mark := Mark(dir)
	RecordSHAs(dir, []string{c})

	if mark != 2 {
		t.Errorf("mark = %d, want 2", mark)
	}
	got := RecordedSince(dir, mark)
	if len(got) != 1 || !got[c] {
		t.Errorf("recorded since the mark = %v, want only the third", got)
	}
	if all := RecordedSince(dir, 0); len(all) != 3 {
		t.Errorf("recorded since 0 = %v, want all three", all)
	}
}
