package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const zeroOid = "0000000000000000000000000000000000000000"

func prepushRoot(t *testing.T, on bool) string {
	t.Helper()
	root := t.TempDir()
	manifest := "[aphrollo]\n"
	if on {
		manifest += "undercover = true\n"
	}
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// git hands pre-push one line per ref: local ref, local oid, remote ref,
// remote oid. Either name reaching the remote carries the tell into it.
func TestRunGatePrepush_RefusesATellInTheLocalOrTheRemoteName(t *testing.T) {
	t.Parallel()
	root := prepushRoot(t, true)
	oid := strings.Repeat("a", 40)
	for _, c := range []struct{ line, name string }{
		{"refs/heads/claude/quirky-faraday " + oid + " refs/heads/claude/quirky-faraday " + zeroOid, "claude/quirky-faraday"},
		{"refs/heads/lane/fix " + oid + " refs/heads/lane/claude-fix " + zeroOid, "lane/claude-fix"},
		{"refs/heads/Claude_x " + oid + " refs/heads/lane/x " + zeroOid, "Claude_x"},
	} {
		stdin := "refs/heads/lane/cairo " + oid + " refs/heads/lane/cairo " + zeroOid + "\n" + c.line + "\n"
		var errb bytes.Buffer
		if code := runGatePrepush(strings.NewReader(stdin), &errb, root); code == 0 {
			t.Errorf("%q: want a refusal, got exit 0", c.line)
		}
		for _, want := range []string{`"` + c.name + `"`, `"claude"`, "lane/<slug>"} {
			if !strings.Contains(errb.String(), want) {
				t.Errorf("%q: refusal %q lacks %q", c.line, errb.String(), want)
			}
		}
	}
}

func TestRunGatePrepush_PassesOrdinaryRefsAndTheDeletionOfATellOne(t *testing.T) {
	t.Parallel()
	root := prepushRoot(t, true)
	oid := strings.Repeat("b", 40)
	stdin := "refs/heads/lane/cairo " + oid + " refs/heads/lane/cairo " + zeroOid + "\n" +
		"refs/heads/lane/air-fix " + oid + " refs/heads/lane/air-fix " + oid + "\n" +
		"(delete) " + zeroOid + " refs/heads/claude/leaked " + oid + "\n"
	var errb bytes.Buffer
	if code := runGatePrepush(strings.NewReader(stdin), &errb, root); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestRunGatePrepush_IsInertWhenTheRepoNeverAsked(t *testing.T) {
	t.Parallel()
	oid := strings.Repeat("c", 40)
	stdin := "refs/heads/claude/x " + oid + " refs/heads/claude/x " + zeroOid + "\n"
	for _, root := range []string{prepushRoot(t, false), ""} {
		var errb bytes.Buffer
		if code := runGatePrepush(strings.NewReader(stdin), &errb, root); code != 0 {
			t.Errorf("root %q: exit %d: %s", root, code, errb.String())
		}
	}
}

// prepushCommitRepo is an undercover (or not) repo on main with one ordinary
// commit; commit adds one more, git's global options before the verb and
// commit's own options after it, and answers its sha.
func prepushCommitRepo(t *testing.T, on bool) (repo string, commit func(global []string, opts ...string) string) {
	t.Helper()
	repo, cfg := undercoverShimRepo(t, on)
	commit = func(global []string, opts ...string) string {
		t.Helper()
		args := append(append(append([]string{}, global...), "commit", "-q", "--allow-empty", "-m", "work"), opts...)
		runRealGit(t, cfg.realGit, repo, args...)
		return strings.TrimSpace(gitShimOut(cfg.realGit, repo, "rev-parse", "HEAD"))
	}
	return repo, commit
}

func pushLine(sha, remoteSha string) string {
	return "refs/heads/lane/x " + sha + " refs/heads/lane/x " + remoteSha + "\n"
}

// A pushed commit carries its author, its committer and its trailers into
// the remote for good; the ref name being clean does not make them clean.
func TestRunGatePrepush_RefusesACommitWhoseIdentityOrTrailerCarriesATell(t *testing.T) {
	tool := []string{"-c", "user.name=Claude", "-c", "user.email=noreply@anthropic.com"}
	for _, c := range []struct {
		name   string
		global []string
		opts   []string
		field  string
	}{
		{"author", nil, []string{"--author", "Claude <noreply@anthropic.com>"}, "author"},
		{"committer", tool, []string{"--author", "Jane <jane@example.com>"}, "committer"},
		{"trailer", nil, []string{"-m", "Co-authored-by: Claude <noreply@anthropic.com>"}, "Co-authored-by trailer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo, commit := prepushCommitRepo(t, true)
			sha := commit(c.global, c.opts...)
			commit(nil)
			tip := commit(nil)
			// A new ref, and a remote tip this clone never fetched.
			for _, remote := range []string{zeroOid, strings.Repeat("1", 40)} {
				var errb bytes.Buffer
				if code := runGatePrepush(strings.NewReader(pushLine(tip, remote)), &errb, repo); code == 0 {
					t.Fatalf("remote %s: want a refusal, got exit 0", remote)
				}
				for _, want := range []string{sha[:12], c.field, "noreply@anthropic.com", "git config user.name"} {
					if !strings.Contains(errb.String(), want) {
						t.Errorf("remote %s: refusal %q lacks %q", remote, errb.String(), want)
					}
				}
			}
		})
	}
}

func TestRunGatePrepush_PassesPeoplesCommitsAndAPersonCoAuthoring(t *testing.T) {
	repo, commit := prepushCommitRepo(t, true)
	commit(nil)
	sha := commit(nil, "-m", "Co-authored-by: Claudia Airey <claudia@example.com>")
	var errb bytes.Buffer
	if code := runGatePrepush(strings.NewReader(pushLine(sha, zeroOid)), &errb, repo); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

// Only the commits the push SENDS are judged: history the remote already
// holds was judged when it went, or predates the rule.
func TestRunGatePrepush_JudgesOnlyTheCommitsThePushSends(t *testing.T) {
	repo, commit := prepushCommitRepo(t, true)
	realGit := realGitForTest(t)
	old := commit([]string{"-c", "user.name=Claude", "-c", "user.email=noreply@anthropic.com"})
	runRealGit(t, realGit, repo, "update-ref", "refs/remotes/origin/main", old)
	sha := commit(nil)
	for name, line := range map[string]string{
		"new branch":   pushLine(sha, zeroOid),
		"fast-forward": pushLine(sha, old),
	} {
		var errb bytes.Buffer
		if code := runGatePrepush(strings.NewReader(line), &errb, repo); code != 0 {
			t.Errorf("%s: exit %d: %s", name, code, errb.String())
		}
	}
}

func TestRunGatePrepush_IgnoresCommitIdentitiesWhenTheRepoNeverAsked(t *testing.T) {
	repo, commit := prepushCommitRepo(t, false)
	sha := commit([]string{"-c", "user.name=Claude", "-c", "user.email=noreply@anthropic.com"})
	var errb bytes.Buffer
	if code := runGatePrepush(strings.NewReader(pushLine(sha, zeroOid)), &errb, repo); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}
