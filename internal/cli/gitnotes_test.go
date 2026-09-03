package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// notesRepo builds a repo with one commit carrying a gate note, and a bare
// remote to push at. It returns the working clone and the bare remote.
func notesRepo(t *testing.T) (repo, remote string) {
	t.Helper()
	isolateGit(t)
	remote = filepath.Join(t.TempDir(), "remote.git")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	run(remote, "init", "-q", "--bare")

	repo = t.TempDir()
	run(repo, "init", "-q", "-b", "main")
	run(repo, "config", "user.email", "t@t")
	run(repo, "config", "user.name", "t")
	run(repo, "config", "commit.gpgsign", "false")
	run(repo, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "one")
	run(repo, "notes", "--ref=gate", "add", "-m", "green deadbeef", "HEAD")
	return repo, remote
}

func remoteHasNotesRef(t *testing.T, remote string) bool {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", remote, "rev-parse", "--verify", "refs/notes/gate")
	return cmd.Run() == nil
}

// The note is the whole channel to CI, and a note nobody pushed reaches no
// runner. Pushing it by hand is a step nobody remembers, so the shim that
// already brokers every push does it.
func TestGitShimPushesTheGateNotesRefWithTheBranch(t *testing.T) {
	withDirectGitShim(t)
	repo, remote := notesRepo(t)
	t.Chdir(repo)

	var out, errb bytes.Buffer
	cfg := gitShimConfig{waitBudget: 5 * time.Second, pollInterval: 10 * time.Millisecond, realGit: "git"}
	if code := runGitShim([]string{"push", "origin", "main"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("push exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !remoteHasNotesRef(t, remote) {
		t.Fatalf("the notes ref did not reach the remote\nstderr: %s", errb.String())
	}
}

// A push is the operator's command, not the gate's. A notes ref that cannot
// be pushed (no permission on a protected remote, a ref another box moved)
// must never turn a successful push into a failure.
func TestAFailingNotesPushDoesNotFailTheBranchPush(t *testing.T) {
	withDirectGitShim(t)
	repo, remote := notesRepo(t)
	t.Chdir(repo)
	gitDoT(t, repo, "push", "-q", "origin", "main")
	// The remote's notes ref now points somewhere the local one does not
	// descend from, so the notes push is rejected as a non-fast-forward
	// while the branch push has nothing left to do and succeeds.
	head := gitOutLine(t, repo, "rev-parse", "HEAD")
	gitDoT(t, repo, "--git-dir", remote, "update-ref", "refs/notes/gate", head)

	var out, errb bytes.Buffer
	cfg := gitShimConfig{waitBudget: 5 * time.Second, pollInterval: 10 * time.Millisecond, realGit: "git"}
	code := runGitShim([]string{"push", "origin", "main"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("a rejected notes push must not change the push's own exit code, got %d\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "refs/notes/gate") {
		t.Errorf("a note that did not reach the remote must say so once: %q", errb.String())
	}
}

func gitDoT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func gitOutLine(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// A repo with no gate note has nothing to push, and must not spend a git
// process at every push discovering that.
func TestNoNotesRefMeansNoNotesPush(t *testing.T) {
	withDirectGitShim(t)
	repo, remote := notesRepo(t)
	t.Chdir(repo)
	cmd := exec.Command("git", "update-ref", "-d", "refs/notes/gate")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dropping the notes ref: %s", out)
	}

	var out, errb bytes.Buffer
	cfg := gitShimConfig{waitBudget: 5 * time.Second, pollInterval: 10 * time.Millisecond, realGit: "git"}
	if code := runGitShim([]string{"push", "origin", "main"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("push exit = %d\nstderr: %s", code, errb.String())
	}
	if remoteHasNotesRef(t, remote) {
		t.Fatal("a repo with no note must push none")
	}
}

// TestPushRemote_SkipsAPushOptionsSeparateArgvToken pins the defect: `git
// push -o ci.skip origin main` used to resolve remote as "ci.skip" (the
// push option's own VALUE, one argv token after "-o") instead of "origin",
// because pushRemote only knew to skip a "-"-prefixed token, never a
// following token that belongs to it. The gate-note follow-up push then
// silently failed against a nonexistent remote named "ci.skip" while the
// branch push itself (unaffected, given the original argv) succeeded.
func TestPushRemote_SkipsAPushOptionsSeparateArgvToken(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"short -o with a separate value token", []string{"-o", "ci.skip", "origin", "main"}, "origin"},
		{"long --push-option with a separate value token", []string{"--push-option", "ci.skip", "origin", "main"}, "origin"},
		{"--push-option=value is already self-contained", []string{"--push-option=ci.skip", "origin", "main"}, "origin"},
		{"--receive-pack with a separate value token", []string{"--receive-pack", "/opt/git/git-receive-pack", "origin", "main"}, "origin"},
		{"--exec with a separate value token", []string{"--exec", "/opt/git/git-receive-pack", "origin", "main"}, "origin"},
		{"--recurse-submodules with a separate value token", []string{"--recurse-submodules", "on-demand", "origin", "main"}, "origin"},
		{"-u takes no value at all", []string{"-u", "origin", "main"}, "origin"},
		{"--repo's own value IS the remote, not discarded", []string{"--repo", "origin", "main"}, "origin"},
		{"bare push with no remote falls back to origin", []string{}, "origin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pushRemote(c.args); got != c.want {
				t.Fatalf("pushRemote(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}
