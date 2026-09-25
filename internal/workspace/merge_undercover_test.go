package workspace

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// mergeUndercoverRepo is a repo with main on origin and lane/x checked out
// one ordinary commit ahead, declaring undercover (or not). commit adds one
// more commit on the lane: git's global options, then commit's own.
func mergeUndercoverRepo(t *testing.T, on bool) (repo string, commit func(global []string, opts ...string) string) {
	t.Helper()
	repo = repoWithRemote(t)
	if on {
		undercoverOn(t, repo)
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("checkout", "-q", "-b", "lane/x")
	commit = func(global []string, opts ...string) string {
		t.Helper()
		run(append(append(append([]string{}, global...), "commit", "-q", "--allow-empty", "-m", "work"), opts...)...)
		return run("rev-parse", "HEAD")
	}
	commit(nil)
	return repo, commit
}

// stubMergeUndercover wires every remote seam merge reads, and records which
// merge call ran and with what body.
func stubMergeUndercover(t *testing.T, title, body string) (plain *bool, withBody *string) {
	t.Helper()
	plain = new(bool)
	withBody = new(string)
	*withBody = "\x00unset"
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 9, URL: "u", State: "OPEN"}, nil },
		func(wt, branch, method string) error { *plain = true; return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })
	prevText, prevBody, prevGate, prevRetro := ghPRText, ghMergePRBody, premergeGate, postMergeRetro
	ghPRText = func(wt, branch string) (string, string, error) { return title, body, nil }
	ghMergePRBody = func(wt, branch, method, b string) error { *withBody = b; return nil }
	premergeGate = func(*Target, io.Writer) error { return nil }
	postMergeRetro = func(string, string, string, int, io.Writer) {}
	t.Cleanup(func() {
		ghPRText, ghMergePRBody, premergeGate, postMergeRetro = prevText, prevBody, prevGate, prevRetro
	})
	return plain, withBody
}

// GitHub's default squash message lists every commit author as a co-author,
// so a PR carrying a tool identity leaks it into main however clean its text.
func TestMerge_RefusesAPRCommitWhoseAuthorOrCommitterCarriesATell(t *testing.T) {
	tool := []string{"-c", "user.name=Claude", "-c", "user.email=noreply@anthropic.com"}
	for _, c := range []struct {
		name   string
		global []string
		opts   []string
	}{
		{"author", nil, []string{"--author", "Claude <noreply@anthropic.com>"}},
		{"committer", tool, []string{"--author", "Jane <jane@example.com>"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo, commit := mergeUndercoverRepo(t, true)
			sha := commit(c.global, c.opts...)
			commit(nil)
			plain, withBody := stubMergeUndercover(t, "Fix the timer", "Fixes it.")
			m, err := MergePlan(targetFor(repo, "lane/x"), "squash", false)
			if err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			err = m.Apply(&out, &errb)
			if err == nil {
				t.Fatal("want a refusal, got a merge")
			}
			for _, want := range []string{sha[:12], c.name, "noreply@anthropic.com"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q lacks %q", err, want)
				}
			}
			if *plain || *withBody != "\x00unset" {
				t.Error("the merge ran anyway")
			}
		})
	}
}

// The squash body is passed explicitly, so GitHub never generates one; a
// trailing tool footer on the PR body does not travel into it.
func TestMerge_PassesAnExplicitSquashBodyWithTheFooterStripped(t *testing.T) {
	repo, commit := mergeUndercoverRepo(t, true)
	commit(nil, "-m", "Co-authored-by: Claudia Airey <claudia@example.com>")
	plain, withBody := stubMergeUndercover(t, "Fix the timer", "Fixes the retry timer.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)")
	m, err := MergePlan(targetFor(repo, "lane/x"), "squash", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if *plain {
		t.Error("the merge let GitHub generate the squash body")
	}
	if *withBody != "Fixes the retry timer." {
		t.Errorf("squash body = %q, want the PR body without its footer", *withBody)
	}
}

func TestMerge_AnEmptyPRBodySquashesWithTheTitle(t *testing.T) {
	repo, _ := mergeUndercoverRepo(t, true)
	_, withBody := stubMergeUndercover(t, "Fix the timer", "")
	m, err := MergePlan(targetFor(repo, "lane/x"), "squash", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if *withBody != "Fix the timer" {
		t.Errorf("squash body = %q, want the title", *withBody)
	}
}

func TestMerge_RefusesAPRBodyWithATellItCannotStrip(t *testing.T) {
	repo, _ := mergeUndercoverRepo(t, true)
	plain, withBody := stubMergeUndercover(t, "Fix the timer", "Claude wrote this.\n\nFixes it.")
	m, err := MergePlan(targetFor(repo, "lane/x"), "squash", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = m.Apply(&out, &errb)
	if err == nil || !strings.Contains(err.Error(), "Claude wrote this.") {
		t.Fatalf("want a refusal quoting the line, got %v", err)
	}
	if *plain || *withBody != "\x00unset" {
		t.Error("the merge ran anyway")
	}
}

// A rebase merge carries no body; the commits still have to be clean.
func TestMerge_ARebaseMergeChecksCommitsAndPassesNoBody(t *testing.T) {
	repo, _ := mergeUndercoverRepo(t, true)
	plain, withBody := stubMergeUndercover(t, "Fix the timer", "Fixes it.")
	m, err := MergePlan(targetFor(repo, "lane/x"), "rebase", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !*plain || *withBody != "\x00unset" {
		t.Errorf("rebase: plain=%v body=%q, want the plain merge call", *plain, *withBody)
	}
}

func TestMerge_UndercoverIsInertWhenTheRepoNeverAsked(t *testing.T) {
	repo, commit := mergeUndercoverRepo(t, false)
	commit([]string{"-c", "user.name=Claude", "-c", "user.email=noreply@anthropic.com"})
	plain, withBody := stubMergeUndercover(t, "Fix the timer", "Generated with Claude Code")
	m, err := MergePlan(targetFor(repo, "lane/x"), "squash", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !*plain || *withBody != "\x00unset" {
		t.Errorf("plain=%v body=%q, want GitHub's own merge untouched", *plain, *withBody)
	}
}
