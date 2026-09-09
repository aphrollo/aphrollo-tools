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
		cmd := fixtureGit(args...)
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
	cmd := fixtureGit(args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func gitOutLine(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := fixtureGit(args...)
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
	cmd := fixtureGit("update-ref", "-d", "refs/notes/gate")
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

// TestPushRemote_ResolvesTheRemoteFromRepoInItsEqualsForm pins the second
// half of the defect 7b5d8f4 left open: pushRemote handles the two-token
// form `--repo origin main` by falling through to the ordinary positional
// case, but `--repo=origin main` is a single self-contained token, and the
// general "-"-prefixed case discards it outright — so "main" was captured
// as the remote instead of "origin". The other pushValueFlags entries do
// not have this gap: their own "=" forms are already self-contained tokens
// the general case correctly discards, since their VALUE is never the
// remote, so this test also pins that both forms of those flags agree.
func TestPushRemote_ResolvesTheRemoteFromRepoInItsEqualsForm(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--repo=origin resolves the remote from its equals form", []string{"--repo=origin", "main"}, "origin"},
		{"--repo=origin agrees with the two-token form", []string{"--repo", "origin", "main"}, "origin"},
		{"--receive-pack=path agrees with its two-token form", []string{"--receive-pack=/opt/git/git-receive-pack", "origin", "main"}, "origin"},
		{"--exec=path agrees with its two-token form", []string{"--exec=/opt/git/git-receive-pack", "origin", "main"}, "origin"},
		{"--recurse-submodules=on-demand agrees with its two-token form", []string{"--recurse-submodules=on-demand", "origin", "main"}, "origin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pushRemote(c.args); got != c.want {
				t.Fatalf("pushRemote(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// TestPushRemote_TreatsATokenAfterDoubleDashAsPositionalEvenIfDashPrefixed
// pins that pushRemote respects git's own "--" separator: everything after
// it is a positional argument, even one that happens to start with "-", and
// must never be matched against pushValueFlags or discarded by the general
// "-"-prefixed case as if it were still a flag.
func TestPushRemote_TreatsATokenAfterDoubleDashAsPositionalEvenIfDashPrefixed(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"a remote-position token after -- is positional despite starting with a dash", []string{"--", "-o", "main"}, "-o"},
		{"a refspec after -- does not change which token already resolved the remote", []string{"origin", "--", "-o"}, "origin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pushRemote(c.args); got != c.want {
				t.Fatalf("pushRemote(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// TestPushRemote_StaysOutOfADeleteMirrorOrAllPush pins each of the four
// disjuncts that decide the "this shape is not worth touching" bailout on
// its own: a bare `git push --delete`/`-d`/`--mirror`/`--all` must resolve
// no remote at all (an empty string, never falling back to "origin"), and
// each case below carries exactly one of the four flags so only that one
// disjunct is what makes the case match — the other three are false for
// every one of these inputs.
func TestPushRemote_StaysOutOfADeleteMirrorOrAllPush(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--delete alone resolves no remote", []string{"--delete"}, ""},
		{"-d alone resolves no remote", []string{"-d"}, ""},
		{"--mirror alone resolves no remote", []string{"--mirror"}, ""},
		{"--all alone resolves no remote", []string{"--all"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pushRemote(c.args); got != c.want {
				t.Fatalf("pushRemote(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// `git -C <other repo> push` is a push against THAT repo, and the note that
// belongs with it is that repo's. The shim resolved the note from its own
// process cwd instead, so a push into a temp repo sent THIS checkout's
// refs/notes/gate to THIS checkout's remote: the wrong note, the wrong
// remote, and — with the remote reached over HTTPS and no credential helper
// in scope for the repo the shim was standing in — a blocking `git-askpass`
// dialog on the user's desktop, one per push, in the middle of a test run.
func TestGitShim_PushesTheNoteOfTheRepoDashCNamed(t *testing.T) {
	withDirectGitShim(t)
	elsewhere, elsewhereRemote := notesRepo(t)
	standingIn, standingRemote := notesRepo(t)
	t.Chdir(standingIn)

	var out, errb bytes.Buffer
	cfg := gitShimConfig{waitBudget: 5 * time.Second, pollInterval: 10 * time.Millisecond, realGit: "git"}
	if code := runGitShim([]string{"-C", elsewhere, "push", "origin", "main"}, strings.NewReader(""), &out, &errb, cfg); code != 0 {
		t.Fatalf("push exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !remoteHasNotesRef(t, elsewhereRemote) {
		t.Errorf("the note of the repo -C named did not reach its remote\nstderr: %s", errb.String())
	}
	if remoteHasNotesRef(t, standingRemote) {
		t.Errorf("the shim pushed the note of the repo it was STANDING in — that remote is not the one " +
			"the operator pushed to, and reaching it can cost a credential prompt nobody asked for")
	}
}

// The notes push is best effort and runs behind the operator's push, so it
// has no business asking anyone anything: git's terminal prompt is off, both
// askpass hooks are cleared, and the credential helper is told not to go
// interactive. Without this a push into a repo with no helper in scope opens
// a `git-askpass` window and BLOCKS until a human dismisses it.
func TestGateNotesPushCmd_CanNeverPrompt(t *testing.T) {
	cmd := gateNotesPushCmd("git", t.TempDir(), "origin")

	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"credential.interactive=false", "core.askPass="} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv = %q, want %q — a helper that opens a window blocks the push behind it", joined, want)
		}
	}
	env := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS="} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q — every path git can take to a prompt has to be closed, not most of them", want)
		}
	}
}
