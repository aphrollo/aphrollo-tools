package workspace

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withOrigin adds a GitHub-shaped origin remote to a repo built by initRepo,
// the precondition every ghAPI* helper needs (githubOwnerRepo reads it).
func withOrigin(t *testing.T, repo, owner, name string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin",
		"https://github.com/"+owner+"/"+name+".git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
}

// fakeGhAPIScript writes a `gh` script in t.TempDir() that answers `api
// <path>` calls from a fixed table (path -> {stdout, exitCode}), and
// prepends that dir to PATH. Anything not in the table exits 1 with no
// output — an unexpected call fails loudly rather than silently matching.
// POSIX only: every test here targets the REST path-matching logic, not
// process portability, which fakeGh/fakeGHPrinting already cover elsewhere.
func fakeGhAPIScript(t *testing.T, byPath map[string]struct {
	stdout string
	exit   int
}) {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("#!/bin/sh\ncase \"$2\" in\n")
	for path, r := range byPath {
		fmt.Fprintf(&b, "  \"%s\") printf '%%s' '%s'; exit %d ;;\n", path, r.stdout, r.exit)
	}
	b.WriteString("  *) exit 1 ;;\nesac\n")
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGithubOwnerRepo_ParsesOwnerAndRepo(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	owner, name, ok := githubOwnerRepo(repo)
	if !ok || owner != "acme" || name != "widgets" {
		t.Fatalf("githubOwnerRepo = (%q, %q, %v), want (acme, widgets, true)", owner, name, ok)
	}
}

func TestGithubOwnerRepo_NonGithubRemoteIsNotOK(t *testing.T) {
	repo := initRepo(t)
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin", "https://example.com/acme/widgets.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	if _, _, ok := githubOwnerRepo(repo); ok {
		t.Fatal("githubOwnerRepo ok=true for a non-github remote")
	}
}

func TestGhAPIFindPR_NoneFound(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIScript(t, map[string]struct {
		stdout string
		exit   int
	}{
		"repos/acme/widgets/pulls": {stdout: "", exit: 0},
	})
	n, ok, err := ghAPIFindPR(repo, "lane/x")
	if err != nil || ok || n != 0 {
		t.Fatalf("ghAPIFindPR = (%d, %v, %v), want (0, false, nil)", n, ok, err)
	}
}

func TestGhAPIFindPR_Found(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIScript(t, map[string]struct {
		stdout string
		exit   int
	}{
		"repos/acme/widgets/pulls": {stdout: "42", exit: 0},
	})
	n, ok, err := ghAPIFindPR(repo, "lane/x")
	if err != nil || !ok || n != 42 {
		t.Fatalf("ghAPIFindPR = (%d, %v, %v), want (42, true, nil)", n, ok, err)
	}
}

func TestGhAPIGetPR_ParsesFieldsIncludingUnknownMergeable(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	body := `{"number":42,"html_url":"https://github.com/acme/widgets/pull/42","state":"open","draft":false,"merged":false,"mergeable":null,"mergeable_state":"unknown","head":{"ref":"lane/x","sha":"deadbeef"}}`
	fakeGhAPIScript(t, map[string]struct {
		stdout string
		exit   int
	}{
		"repos/acme/widgets/pulls/42": {stdout: body, exit: 0},
	})
	p, err := ghAPIGetPR(repo, 42)
	if err != nil {
		t.Fatalf("ghAPIGetPR: %v", err)
	}
	info := p.info()
	if info.Number != 42 || info.State != "OPEN" || info.Mergeable != "UNKNOWN" || info.MergeStateStatus != "UNKNOWN" {
		t.Fatalf("info() = %+v, want OPEN/UNKNOWN/UNKNOWN", info)
	}
	if p.Head.SHA != "deadbeef" {
		t.Fatalf("Head.SHA = %q, want deadbeef", p.Head.SHA)
	}
}

func TestGhAPIGetPR_MergedAndClean(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	yes := true
	_ = yes
	body := `{"number":7,"html_url":"https://github.com/acme/widgets/pull/7","state":"closed","draft":false,"merged":true,"mergeable":true,"mergeable_state":"clean","head":{"ref":"lane/y","sha":"cafef00d"}}`
	fakeGhAPIScript(t, map[string]struct {
		stdout string
		exit   int
	}{
		"repos/acme/widgets/pulls/7": {stdout: body, exit: 0},
	})
	p, err := ghAPIGetPR(repo, 7)
	if err != nil {
		t.Fatalf("ghAPIGetPR: %v", err)
	}
	if p.state() != "MERGED" {
		t.Fatalf("state() = %q, want MERGED (merged=true beats state=closed)", p.state())
	}
	if p.mergeableWord() != "MERGEABLE" || p.mergeStateStatus() != "CLEAN" {
		t.Fatalf("mergeableWord/mergeStateStatus = %q/%q, want MERGEABLE/CLEAN", p.mergeableWord(), p.mergeStateStatus())
	}
}

func TestGhAPIViewByBranch_AbsenceIsNilNil(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIScript(t, map[string]struct {
		stdout string
		exit   int
	}{
		"repos/acme/widgets/pulls": {stdout: "", exit: 0},
	})
	p, err := ghAPIViewByBranch(repo, "lane/x")
	if err != nil || p != nil {
		t.Fatalf("ghAPIViewByBranch = (%+v, %v), want (nil, nil)", p, err)
	}
}

func TestGhAPIViewByRef_NumericRefFetchesDirectly(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	body := `{"number":9,"html_url":"https://github.com/acme/widgets/pull/9","state":"open","head":{"ref":"lane/z","sha":"abc"}}`
	fakeGhAPIScript(t, map[string]struct {
		stdout string
		exit   int
	}{
		"repos/acme/widgets/pulls/9": {stdout: body, exit: 0},
	})
	p, err := ghAPIViewByRef(repo, "9")
	if err != nil || p == nil || p.Number != 9 {
		t.Fatalf("ghAPIViewByRef(9) = (%+v, %v), want number 9", p, err)
	}
}

func TestFillTitleBody_SingleCommitUsesItsSubjectAndBody(t *testing.T) {
	repo := initRepo(t)
	if out, err := exec.Command("git", "-C", repo, "checkout", "-q", "-b", "lane/x").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "add f\n\nbecause reasons")
	title, body := fillTitleBody(repo, "main", "lane/x")
	if title != "add f" {
		t.Fatalf("title = %q, want %q", title, "add f")
	}
	if body != "because reasons" {
		t.Fatalf("body = %q, want %q", body, "because reasons")
	}
}

// stubRequireGH swaps requireGH for the duration of a test, restored after.
func stubRequireGH(t *testing.T, fn func() error) {
	t.Helper()
	old := requireGH
	requireGH = fn
	t.Cleanup(func() { requireGH = old })
}

// TestRequireGHReal_RefusesWhenGHMissing proves the real requireGH refuses
// with a fix line when gh is not on PATH at all — the "fail loud at the
// verb" half of #880.
func TestRequireGHReal_RefusesWhenGHMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := requireGHReal(); err == nil {
		t.Fatal("requireGHReal() = nil, want a refusal for a missing gh")
	}
}

// TestRequireGHReal_ReadyWhenGHAnswersREST proves requireGH does not demand
// GraphQL: REST-only is enough, since every verb-level gh call goes through
// REST now.
func TestRequireGHReal_ReadyWhenGHAnswersREST(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1 $2\" in\n  \"api user\") exit 0 ;;\n  \"api graphql\") exit 1 ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := requireGHReal(); err != nil {
		t.Fatalf("requireGHReal() = %v, want nil (REST alone is enough)", err)
	}
}

// TestPRApply_RefusesUpFrontWhenGHNotReady proves PR.Apply checks requireGH
// BEFORE calling any gh seam — the "never fails halfway through, after the
// branch is pushed" half of #880. ghViewPR/ghCreatePR would panic-on-call
// here (both nil), so a call would fail the test as a nil dereference or an
// unexpected outcome either way; the point of the assertion is the error
// text naming the preflight refusal.
func TestPRApply_RefusesUpFrontWhenGHNotReady(t *testing.T) {
	repo := repoWithRemote(t)
	stubRequireGH(t, func() error { return fmt.Errorf("gh is not ready: fake refusal") })
	pr, err := PRPlan(targetFor(repo, "main"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err == nil || !strings.Contains(err.Error(), "fake refusal") {
		t.Fatalf("Apply() = %v, want the preflight refusal", err)
	}
}

// TestMergeApply_RefusesUpFrontWhenGHNotReady is PR.Apply's sibling for
// Merge.Apply.
func TestMergeApply_RefusesUpFrontWhenGHNotReady(t *testing.T) {
	repo := repoWithRemote(t)
	stubRequireGH(t, func() error { return fmt.Errorf("gh is not ready: fake refusal") })
	m, err := MergePlan(targetFor(repo, "feat/x"), "squash", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err == nil || !strings.Contains(err.Error(), "fake refusal") {
		t.Fatalf("Apply() = %v, want the preflight refusal", err)
	}
}

// TestSubmitApply_RefusesUpFrontBeforePushing is Submit.Apply's sibling for
// the same preflight — the case the "never fails halfway through, after the
// branch is pushed" wording in #880 names directly: Submit's own push must
// never run when gh cannot even answer a REST call.
func TestSubmitApply_RefusesUpFrontBeforePushing(t *testing.T) {
	repo := pushedRepo(t)
	stubRequireGH(t, func() error { return fmt.Errorf("gh is not ready: fake refusal") })
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			t.Fatal("Submit.Apply reached a gh seam after the preflight refused")
			return nil, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) {
			t.Fatal("Submit.Apply reached a gh seam after the preflight refused")
			return nil, nil
		},
	)
	s, err := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err == nil || !strings.Contains(err.Error(), "fake refusal") {
		t.Fatalf("Apply() = %v, want the preflight refusal", err)
	}
}

func TestFillTitleBody_MultipleCommitsListsEachSubject(t *testing.T) {
	repo := initRepo(t)
	if out, err := exec.Command("git", "-C", repo, "checkout", "-q", "-b", "lane/y").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	run("add", "a.txt")
	run("commit", "-q", "-m", "first")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b"), 0o644)
	run("add", "b.txt")
	run("commit", "-q", "-m", "second")
	title, body := fillTitleBody(repo, "main", "lane/y")
	if title != "lane/y" {
		t.Fatalf("title = %q, want the branch name for multiple commits", title)
	}
	if !strings.Contains(body, "- first\n") || !strings.Contains(body, "- second\n") {
		t.Fatalf("body = %q, want bullets for both commits", body)
	}
}
