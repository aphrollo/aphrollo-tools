package tdd

import (
	"strings"
	"testing"
)

// classifyRepo commits base (path -> content), then applies change (path ->
// content, "" deletes the path) as a second commit, and returns the root and
// both commit ids. The test's working directory moves into the repo: the
// embed rule ClassifyFile applies reads the checkout relative to it, exactly
// as it does for the commit gate and for CI's classify step.
func classifyRepo(t *testing.T, base, change map[string]string) (root, baseRev, headRev string) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	for p, c := range base {
		write(t, root, p, c)
	}
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "base")
	baseRev = strings.TrimSpace(gitOut(root, "rev-parse", "HEAD"))
	for p, c := range change {
		if c == "" {
			gitDo(t, root, "rm", "-q", p)
			continue
		}
		write(t, root, p, c)
	}
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-q", "--allow-empty", "-m", "change")
	headRev = strings.TrimSpace(gitOut(root, "rev-parse", "HEAD"))
	t.Chdir(root)
	return root, baseRev, headRev
}

const classifyGoBase = "package p\n\n// F returns one.\nfunc F() int { return 1 }\n"

func wantClass(t *testing.T, root, base, head string, want DiffClass) {
	t.Helper()
	got, err := ClassifyDiff(root, base, head)
	if got != want {
		t.Fatalf("ClassifyDiff = %q (err %v), want %q", got, err, want)
	}
}

// Prose only: top-level and docs/ markdown, the licence and .gitignore.
func TestClassifyDiff_ProseOnlyIsDocsOnly(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{"README.md": "a\n", "docs/x.md": "a\n", "docs/flow.svg": "a\n", "LICENSE": "a\n", ".gitignore": "a\n", "p/p.go": classifyGoBase},
		map[string]string{"README.md": "b\n", "docs/x.md": "b\n", "docs/flow.svg": "b\n", "LICENSE": "b\n", ".gitignore": "b\n"})
	wantClass(t, root, base, head, DiffDocsOnly)
}

// A markdown file a //go:embed directive compiles into the binary is code,
// wherever it sits.
func TestClassifyDiff_EmbeddedMarkdownIsCode(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{
			"p/p.go":     "package p\n\nimport _ \"embed\"\n\n//go:embed skill.md\nvar Skill string\n",
			"p/skill.md": "old\n",
		},
		map[string]string{"p/skill.md": "new\n"})
	wantClass(t, root, base, head, DiffCode)
}

func TestClassifyDiff_GoCommentOnlyWithProseIsCommentOnly(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{"README.md": "a\n", "p/p.go": classifyGoBase},
		map[string]string{"README.md": "b\n", "p/p.go": strings.Replace(classifyGoBase, "returns one", "returns the number one", 1)})
	wantClass(t, root, base, head, DiffCommentOnly)
}

func TestClassifyDiff_GoCodeChangeIsCode(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{"p/p.go": classifyGoBase},
		map[string]string{"p/p.go": strings.Replace(classifyGoBase, "return 1", "return 2", 1)})
	wantClass(t, root, base, head, DiffCode)
}

// A test file is never comment-only: a Go example's `// Output:` comment is
// an assertion, and the gate never fast-paths a staged test either.
func TestClassifyDiff_TestFileCommentChangeIsCode(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{"p/p_test.go": "package p\n\n// old\n"},
		map[string]string{"p/p_test.go": "package p\n\n// new\n"})
	wantClass(t, root, base, head, DiffCode)
}

// A new or deleted source file has no pair of blobs to compare.
func TestClassifyDiff_AddedOrDeletedSourceIsCode(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{"p/p.go": classifyGoBase},
		map[string]string{"p/q.go": "package p\n\n// only a comment\n"})
	wantClass(t, root, base, head, DiffCode)

	root, base, head = classifyRepo(t,
		map[string]string{"p/p.go": classifyGoBase, "p/q.go": "package p\n\n// only a comment\n"},
		map[string]string{"p/q.go": ""})
	wantClass(t, root, base, head, DiffCode)
}

// Only .github/** (plus prose) changed: the workflow pin tests are what can
// still fail, not the suites.
func TestClassifyDiff_WorkflowOnlyWithProseIsWorkflowOnly(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{".github/workflows/pipeline.yml": "a: 1\n", ".github/dependabot.yml": "a: 1\n", "README.md": "a\n", "p/p.go": classifyGoBase},
		map[string]string{".github/workflows/pipeline.yml": "a: 2\n", ".github/dependabot.yml": "a: 2\n", "README.md": "b\n"})
	wantClass(t, root, base, head, DiffWorkflowOnly)
}

// Neither lighter path covers both halves of a workflow edit sitting next to
// a comment-only one, so the heaviest path runs.
func TestClassifyDiff_WorkflowPlusCommentOnlyIsCode(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{".github/workflows/pipeline.yml": "a: 1\n", "p/p.go": classifyGoBase},
		map[string]string{".github/workflows/pipeline.yml": "a: 2\n", "p/p.go": strings.Replace(classifyGoBase, "returns one", "returns 1", 1)})
	wantClass(t, root, base, head, DiffCode)
}

// Non-code files that are not prose -- a test fixture, the linter's own
// configuration, a deploy script, a law -- each change what a check does,
// so none of them is documentation.
func TestClassifyDiff_NonProseDataIsCode(t *testing.T) {
	for _, p := range []string{"p/testdata/golden.txt", ".golangci.yml", "deploy/deploy-prod.sh", ".ratchet/laws/x.toml", "p/testdata/fixture.md"} {
		t.Run(p, func(t *testing.T) {
			root, base, head := classifyRepo(t,
				map[string]string{p: "a\n"},
				map[string]string{p: "b\n"})
			wantClass(t, root, base, head, DiffCode)
		})
	}
}

// A base that is not a commit in this clone -- the all-zero sha of a first
// push, a sha a force-push dropped -- must never pick a fast path.
func TestClassifyDiff_UnresolvableBaseIsCodeAndSaysWhy(t *testing.T) {
	root, _, head := classifyRepo(t,
		map[string]string{"README.md": "a\n"},
		map[string]string{"README.md": "b\n"})
	got, err := ClassifyDiff(root, "0000000000000000000000000000000000000000", head)
	if got != DiffCode || err == nil {
		t.Fatalf("an unresolvable base must answer code with an error, got %q, %v", got, err)
	}
}

// An empty diff proves nothing about what the change is, so it is code.
func TestClassifyDiff_EmptyDiffIsCode(t *testing.T) {
	root, base, head := classifyRepo(t, map[string]string{"README.md": "a\n"}, nil)
	wantClass(t, root, base, head, DiffCode)
}

// The embed rule reads the checkout, so a head that is not the checked-out
// commit cannot be judged by it.
func TestClassifyDiff_HeadThatIsNotTheCheckoutIsCode(t *testing.T) {
	root, base, head := classifyRepo(t,
		map[string]string{"README.md": "a\n"},
		map[string]string{"README.md": "b\n"})
	gitDo(t, root, "checkout", "-q", base)
	got, err := ClassifyDiff(root, base, head)
	if got != DiffCode || err == nil {
		t.Fatalf("a head other than the checkout must answer code with an error, got %q, %v", got, err)
	}
}
