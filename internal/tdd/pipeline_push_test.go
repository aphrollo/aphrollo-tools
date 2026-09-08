package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRootForTest is THIS repo's own root, which a workflow-pinning test
// reads its assertions out of.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	root := RepoRoot(".")
	if root == "" {
		t.Fatal("these tests read this repo's own tree and could not find its root")
	}
	return root
}

// repoFile reads one of THIS repo's own tracked files.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{repoRootForTest(t)}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Every check job in the pipeline gates its real steps on the `changes` job's
// diff, and that diff was computed from `github.event.pull_request.base.sha`
// alone. On a push there is no pull request, so the expression renders empty
// and the job dies on `git diff --name-only ""` with
//
//	fatal: ambiguous argument '': unknown revision or path not in the working tree.
//
// exit 128. Every job that needs it -- test, lint, scan, benchmarks,
// docs-check and build -- is then skipped. A push to main therefore reported
// a red pipeline having run no check at all, which is the worst of both: no
// coverage, and a failure signal that says nothing about the code. (The
// mutants tripwire itself moved to nightly-mutants.yml, issue #334, and no
// longer runs on push at all.)
//
// A push carries its own base in `github.event.before`, so every site that
// reads a base must fall back to it.
func TestPipeline_ResolvesADiffBaseOnAPushWhereThereIsNoPullRequest(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")

	sites := 0
	for _, line := range strings.Split(wf, "\n") {
		if !strings.Contains(line, "BASE_SHA:") {
			continue
		}
		sites++
		if !strings.Contains(line, "github.event.before") {
			t.Errorf("BASE_SHA line %q names no push fallback — on a push the pull-request expression renders empty and the job dies on `git diff \"\"`", strings.TrimSpace(line))
		}
	}
	if sites == 0 {
		t.Fatal("no BASE_SHA site found in pipeline.yml, so this test proves nothing")
	}
}

// ...and the base a push hands over can still be unusable: the all-zero sha of
// a branch's first push, or a sha the shallow clone does not hold after a
// force-push. The resolver must survive that rather than trade one exit 128
// for another, so it verifies the base is a real commit before diffing.
func TestPipeline_VerifiesTheDiffBaseIsACommitBeforeDiffingAgainstIt(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")

	if !strings.Contains(wf, "rev-parse --verify") {
		t.Error("no site verifies its diff base is a commit: an all-zero `before` sha from a first push would fail the same way the empty one did")
	}
}

// escape-closure asks `gate escape verify-closure` about a PR number. On a
// push that number renders empty and the CLI reports
//
//	no pull requests found for branch "main"
//
// exit 1 — a red job on every merge to main, recording nothing and gating
// nothing. The job is about a pull request, so it must say so in its `if`
// rather than run and fail.
func TestPipeline_DoesNotAskForAPullRequestNumberOnAPush(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")

	start := strings.Index(wf, "\n  escape-closure:")
	if start < 0 {
		t.Fatal("no escape-closure job in pipeline.yml, so this test proves nothing")
	}
	job := wf[start:]
	if end := strings.Index(job[1:], "\n  escape-record:"); end >= 0 {
		job = job[:end]
	}
	if !strings.Contains(job, "github.event_name == 'pull_request'") {
		t.Error("escape-closure does not gate on the pull_request event, so a push runs it with an empty PR number and it fails on every merge to main")
	}
}

// The docs-only fast path skips test, lint, vulncheck, sast, build and the
// mutation tripwire, and it decided on the `.md` suffix alone. This repo
// compiles markdown INTO the binary -- `//go:embed tddskill.md`,
// `agent_*.md`, `ratchet_laws.md`, `sddskill.md`, `style.md` -- so a commit
// changing only what the CLI ships classified as docs and ran no Go check at
// all. ClassifyFile already reads the embed directives for the local gate;
// the workflow cannot, so it draws the conservative line instead: a markdown
// file sitting in a package directory is code, and only a top-level or
// docs/ markdown is docs.
func TestPipeline_DoesNotTreatAPackageLocalMarkdownAsDocsOnly(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")

	if strings.Contains(wf, `grep -qvE '(\.md$|^docs/`) {
		t.Error("the docs-only filter matches every .md by suffix, so a change to an embedded skill or law document skips every Go check")
	}
	if !strings.Contains(wf, `^[^/]*\.md$`) {
		t.Error("the docs-only filter must exempt only TOP-LEVEL markdown; a .md inside a package directory can be a //go:embed target")
	}
}
