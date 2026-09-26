package undercover

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitTell_FindsTheAuthorCommitterOrACoAuthorTrailer(t *testing.T) {
	t.Parallel()
	l := New(nil)
	person := "Jane Doe <jane@example.com>"
	for _, c := range []struct {
		author, committer, message, field string
	}{
		{"Claude <noreply@anthropic.com>", person, "Fix the timer\n", "author"},
		{person, "Claude <noreply@anthropic.com>", "Fix the timer\n", "committer"},
		{person, person, "Fix the timer\n\nCo-authored-by: Claude <noreply@anthropic.com>\n", "Co-authored-by trailer"},
		{person, person, "Fix the timer\n\nco-authored-by:Copilot <175728472+Copilot@users.noreply.github.com>", "Co-authored-by trailer"},
	} {
		h, ok := l.CommitTell(c.author, c.committer, c.message)
		if !ok || h.Field != c.field || h.Tell == "" || h.Value == "" {
			t.Errorf("%+v: got (%+v, %v), want a hit on the %s", c, h, ok, c.field)
		}
	}
}

// A person co-authoring is ordinary, and the message prose is the commit-msg
// gate's business, not this one's.
func TestCommitTell_PassesPeopleAndLeavesProseAlone(t *testing.T) {
	t.Parallel()
	l := New(nil)
	person := "Jane Doe <jane@example.com>"
	for _, msg := range []string{
		"Fix the timer\n\nCo-authored-by: Claudia Airey <claudia@example.com>\n",
		"Fix the timer\n\nSigned-off-by: Jane Doe <jane@example.com>\n",
		"Explain the Co-authored-by convention\n",
	} {
		if h, ok := l.CommitTell(person, "GitHub <noreply@github.com>", msg); ok {
			t.Errorf("%q was refused: %+v", msg, h)
		}
	}
}

func TestConfigIdentities_ReadsTheGlobalAndTheRepoScope(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Jane Doe\n\temail = jane@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Claude"}, {"config", "user.email", "noreply@anthropic.com"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	got := ConfigIdentities(repo)
	if len(got) != 2 || got[0].Scope != "global" || got[0].String() != "Jane Doe <jane@example.com>" ||
		got[1].Scope != "repo" || got[1].String() != "Claude <noreply@anthropic.com>" {
		t.Fatalf("ConfigIdentities = %+v", got)
	}
	if id, tell, ok := New(nil).ConfiguredIdentityTell(repo); !ok || id.Scope != "repo" || tell == "" {
		t.Errorf("ConfiguredIdentityTell = (%+v, %q, %v), want the repo scope", id, tell, ok)
	}
	if got := ConfigIdentities(t.TempDir()); len(got) != 1 || got[0].Scope != "global" {
		t.Errorf("a directory that is not a repo has only the global scope, got %+v", got)
	}
}

func TestConfigIdentities_SkipsAScopeThatSetsNeither(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	if got := ConfigIdentities(t.TempDir()); len(got) != 0 {
		t.Errorf("ConfigIdentities = %+v, want none", got)
	}
	if _, _, ok := New(nil).ConfiguredIdentityTell(t.TempDir()); ok {
		t.Error("no identity carries no tell")
	}
}

func TestRangeTell_FindsTheCommitInTheRangeOnly(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("-c", "user.name=Jane", "-c", "user.email=jane@example.com", "commit", "-q", "--allow-empty", "-m", "base")
	base := strings.TrimSpace(git("rev-parse", "HEAD"))
	git("-c", "user.name=Jane", "-c", "user.email=jane@example.com", "commit", "-q", "--allow-empty", "-m", "work",
		"--author", "Claude <noreply@anthropic.com>")
	bad := strings.TrimSpace(git("rev-parse", "HEAD"))
	git("-c", "user.name=Jane", "-c", "user.email=jane@example.com", "commit", "-q", "--allow-empty", "-m", "more")

	sha, h, hit, err := New(nil).RangeTell("git", repo, nil, base+"..HEAD")
	if err != nil || !hit || sha != bad || h.Field != "author" {
		t.Fatalf("RangeTell = (%q, %+v, %v, %v), want the author of %s", sha, h, hit, err, bad)
	}
	if _, _, hit, err := New(nil).RangeTell("git", repo, nil, bad+"..HEAD"); hit || err != nil {
		t.Errorf("a range past the commit still hit (err %v)", err)
	}
	if _, _, _, err := New(nil).RangeTell("git", repo, nil, "no-such-ref..HEAD"); err == nil {
		t.Error("a range git cannot resolve must be an error")
	}
}
