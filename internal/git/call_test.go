package git

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A verb run by the operator asks git for more than a hook does: a network
// call with a deadline of its own, a call whose stderr belongs in the answer,
// a commit that must see the operator's own GIT_* settings. These pin those
// shapes of one call, and the facts that need no spawn at all.

func TestCombined_AnswersWhatGitSaidOnBothStreams(t *testing.T) {
	c := mustNew(t, repoWithCommit(t))

	out, err := c.Combined("log", "no-such-ref")

	if err == nil {
		t.Fatal("a missing ref resolved")
	}
	if !strings.Contains(out, "no-such-ref") {
		t.Errorf("output %q lacks git's own complaint naming the ref", out)
	}
}

func TestDo_GivesTheCallItsOwnWriters(t *testing.T) {
	c := mustNew(t, repoWithCommit(t))
	var stdout, stderr bytes.Buffer

	err := c.Do(Call{Stdout: &stdout, Stderr: &stderr}, "rev-parse", "HEAD")

	if err != nil || len(strings.TrimSpace(stdout.String())) != 40 || stderr.Len() != 0 {
		t.Fatalf("err = %v, stdout = %q, stderr = %q; want a commit on stdout and nothing else", err, stdout.String(), stderr.String())
	}
	if c.Spawns() != 1 {
		t.Errorf("spawns = %d, want 1", c.Spawns())
	}
}

func TestDo_AnAbsurdlyShortDeadlineEndsTheCall(t *testing.T) {
	c := mustNew(t, repoWithCommit(t))

	err := c.Do(Call{Timeout: time.Nanosecond}, "rev-parse", "HEAD")

	if err == nil {
		t.Error("a call with a one-nanosecond deadline finished")
	}
}

// The operator's own GIT_* settings (an author, an ssh command) belong to a
// verb the operator ran; a hook's client drops them.
func TestInherit_KeepsTheOperatorsGitEnvironmentAndTheDefaultDropsIt(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "config", "user.name", "Configured")
	gitT(t, dir, "config", "user.email", "c@example.invalid")
	t.Setenv("GIT_AUTHOR_NAME", "Operator Override")

	inherit, err := New(dir, Options{Inherit: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := inherit.Output("var", "GIT_AUTHOR_IDENT")
	if err != nil || !strings.Contains(got, "Operator Override") {
		t.Errorf("inheriting client: %q, %v; want the GIT_AUTHOR_NAME in the environment", got, err)
	}

	scrubbed := mustNew(t, dir)
	got, err = scrubbed.Output("var", "GIT_AUTHOR_IDENT")
	if err != nil || strings.Contains(got, "Operator Override") {
		t.Errorf("default client: %q, %v; want the GIT_* variable dropped", got, err)
	}
}

func TestResolveRef_ReadsLooseAndPackedRefsWithoutSpawning(t *testing.T) {
	dir := repoWithCommit(t)
	want := gitT(t, dir, "rev-parse", "HEAD")
	gitT(t, dir, "branch", "loose")
	gitT(t, dir, "branch", "packed")
	gitT(t, dir, "pack-refs", "--all", "--prune")
	gitT(t, dir, "branch", "later")
	c := mustNew(t, dir)

	for _, ref := range []string{"refs/heads/loose", "refs/heads/packed", "refs/heads/later", "HEAD"} {
		if got := c.ResolveRef(ref); got != want {
			t.Errorf("ResolveRef(%q) = %q, want %q", ref, got, want)
		}
	}
	if got := c.ResolveRef("refs/heads/none"); got != "" {
		t.Errorf("a ref that does not exist resolved to %q", got)
	}
	if c.Spawns() != 0 {
		t.Errorf("spawns = %d, want 0: refs sit in files", c.Spawns())
	}
}

func TestRemoteURL_AsksGitOncePerNameAndSaysNothingForAMissingRemote(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	c := mustNew(t, dir)

	for range 3 {
		if got := c.RemoteURL("origin"); got != "https://github.com/acme/widgets.git" {
			t.Fatalf("RemoteURL = %q", got)
		}
	}
	if got := c.RemoteURL("upstream"); got != "" {
		t.Errorf("a remote that does not exist answered %q", got)
	}
	if c.Spawns() != 2 {
		t.Errorf("spawns = %d, want 2 (origin once, upstream once)", c.Spawns())
	}
}
