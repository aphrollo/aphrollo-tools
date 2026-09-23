package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// classifyBothWays builds one diff and asks both sides about it: the commit
// gate's fast-path choice over the STAGED change (tdd.StagedFastPath, what
// Precommit and Mechanical dispatch on), then CI's `gate classify-diff`
// over the same change once committed.
func classifyBothWays(t *testing.T, base, change map[string]string) (local tdd.DiffClass, ci string) {
	t.Helper()
	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	for p, c := range base {
		writeFile(t, filepath.Join(root, filepath.FromSlash(p)), c)
	}
	gitCommitAll(t, root, "base")
	baseRev := gitLine(t, root, "rev-parse", "HEAD")
	for p, c := range change {
		writeFile(t, filepath.Join(root, filepath.FromSlash(p)), c)
	}
	gitRun(t, root, "add", "-A")
	t.Chdir(root)
	local = tdd.StagedFastPath(root)

	gitCommitAll(t, root, "change")
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "classify-diff", baseRev}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("classify-diff exited %d: %s", code, errb.String())
	}
	return local, strings.TrimSpace(out.String())
}

const agreeGo = "package p\n\n// F returns one.\nfunc F() int { return 1 }\n"

// The same diff gets the same class from CI's entry as the commit gate
// takes. A rule changed on one side and not the other fails a row here.
func TestClassifyDiff_AgreesWithTheCommitGate(t *testing.T) {
	cases := []struct {
		name         string
		base, change map[string]string
		want         tdd.DiffClass
	}{
		{"prose", map[string]string{"README.md": "a\n", "docs/x.md": "a\n"},
			map[string]string{"README.md": "b\n", "docs/x.md": "b\n"}, tdd.DiffDocsOnly},
		{"embedded markdown", map[string]string{
			"p/p.go":     "package p\n\nimport _ \"embed\"\n\n//go:embed skill.md\nvar Skill string\n",
			"p/skill.md": "old\n"},
			map[string]string{"p/skill.md": "new\n"}, tdd.DiffCode},
		{"go comment-only with prose", map[string]string{"README.md": "a\n", "p/p.go": agreeGo},
			map[string]string{"README.md": "b\n", "p/p.go": strings.Replace(agreeGo, "returns one", "returns 1", 1)}, tdd.DiffCommentOnly},
		{"rust comment-only", map[string]string{"src/lib.rs": "// old\npub fn f() -> i32 { 1 }\n"},
			map[string]string{"src/lib.rs": "// new\npub fn f() -> i32 { 1 }\n"}, tdd.DiffCommentOnly},
		{"go directive", map[string]string{"p/p.go": "//go:build linux\n\npackage p\n"},
			map[string]string{"p/p.go": "//go:build windows\n\npackage p\n"}, tdd.DiffCode},
		{"go code", map[string]string{"p/p.go": agreeGo},
			map[string]string{"p/p.go": strings.Replace(agreeGo, "return 1", "return 2", 1)}, tdd.DiffCode},
		{"comment-only next to code", map[string]string{"p/p.go": agreeGo, "p/q.go": "package p\n\nvar Q = 1\n"},
			map[string]string{"p/p.go": strings.Replace(agreeGo, "returns one", "returns 1", 1), "p/q.go": "package p\n\nvar Q = 2\n"}, tdd.DiffCode},
		{"test file comment", map[string]string{"p/p_test.go": "package p\n\n// old\n"},
			map[string]string{"p/p_test.go": "package p\n\n// new\n"}, tdd.DiffCode},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			local, ci := classifyBothWays(t, c.base, c.change)
			if local != c.want || ci != string(c.want) {
				t.Fatalf("commit gate %q, CI %q, want both %q", local, ci, c.want)
			}
		})
	}
}

// Where the two sides deliberately differ, each difference is a row here
// rather than an accident: CI narrows docs-only to prose (a fixture or a
// config file changes what a check does), and CI alone has a workflow-only
// path, whose pin tests are what a .github change can break.
func TestClassifyDiff_DiffersFromTheCommitGateOnlyWhereDeclared(t *testing.T) {
	cases := []struct {
		name         string
		base, change map[string]string
		local        tdd.DiffClass
		ci           tdd.DiffClass
	}{
		{"testdata fixture", map[string]string{"p/testdata/golden.txt": "a\n"},
			map[string]string{"p/testdata/golden.txt": "b\n"}, tdd.DiffDocsOnly, tdd.DiffCode},
		{"lint config", map[string]string{".golangci.yml": "a: 1\n"},
			map[string]string{".golangci.yml": "a: 2\n"}, tdd.DiffDocsOnly, tdd.DiffCode},
		{"other workflow", map[string]string{".github/workflows/nightly.yml": "a: 1\n"},
			map[string]string{".github/workflows/nightly.yml": "a: 2\n"}, tdd.DiffDocsOnly, tdd.DiffWorkflowOnly},
		{"pipeline workflow", map[string]string{".github/workflows/pipeline.yml": "a: 1\n"},
			map[string]string{".github/workflows/pipeline.yml": "a: 2\n"}, tdd.DiffCode, tdd.DiffWorkflowOnly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			local, ci := classifyBothWays(t, c.base, c.change)
			if local != c.local || ci != string(c.ci) {
				t.Fatalf("commit gate %q, CI %q, want %q and %q", local, ci, c.local, c.ci)
			}
		})
	}
}

// A base the clone does not hold still prints a class, and it is code: the
// workflow reads stdout, so an error must never read as a fast path.
func TestGateClassifyDiff_UnresolvableBasePrintsCode(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	writeFile(t, filepath.Join(root, "README.md"), "a\n")
	gitCommitAll(t, root, "base")
	t.Chdir(root)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "classify-diff", "0000000000000000000000000000000000000000"}, strings.NewReader(""), &out, &errb)
	if code != 0 || strings.TrimSpace(out.String()) != "code" {
		t.Fatalf("exit %d, stdout %q, want exit 0 and %q", code, out.String(), "code")
	}
	if !strings.Contains(errb.String(), "0000000000000000000000000000000000000000") {
		t.Fatalf("stderr must say which base failed to resolve, got %q", errb.String())
	}
}

// Outside any repository there is no diff to read, and the answer is code.
func TestGateClassifyDiff_OutsideARepoPrintsCode(t *testing.T) {
	isolateGit(t)
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "classify-diff", "HEAD~1"}, strings.NewReader(""), &out, &errb)
	if code != 0 || strings.TrimSpace(out.String()) != "code" {
		t.Fatalf("exit %d, stdout %q, want exit 0 and %q", code, out.String(), "code")
	}
}

func TestGateClassifyDiff_JSONCarriesTheClassAndTheReason(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	writeFile(t, filepath.Join(root, "README.md"), "a\n")
	gitCommitAll(t, root, "base")
	t.Chdir(root)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "classify-diff", "--json", "nope"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var got struct{ Class, Reason string }
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if got.Class != "code" || !strings.Contains(got.Reason, "nope") {
		t.Fatalf("got %+v, want class code and a reason naming the base", got)
	}
}

// No base at all is a usage error, not a class.
func TestGateClassifyDiff_NoBaseIsAUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "classify-diff"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Fatalf("a usage error must print no class, got %q", out.String())
	}
}

// Run from a package directory, the embed rule must still resolve against
// the repository root: an embedded markdown change stays code.
func TestGateClassifyDiff_FromASubdirectoryStillSeesTheEmbed(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	writeFile(t, filepath.Join(root, "p", "p.go"), "package p\n\nimport _ \"embed\"\n\n//go:embed skill.md\nvar Skill string\n")
	writeFile(t, filepath.Join(root, "p", "skill.md"), "old\n")
	gitCommitAll(t, root, "base")
	baseRev := gitLine(t, root, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(root, "p", "skill.md"), "new\n")
	gitCommitAll(t, root, "change")
	t.Chdir(filepath.Join(root, "p"))

	var out, errb bytes.Buffer
	Run([]string{"gate", "classify-diff", baseRev}, strings.NewReader(""), &out, &errb)
	if got := strings.TrimSpace(out.String()); got != "code" {
		t.Fatalf("an embedded markdown change classified from a subdirectory printed %q, want code (stderr %s)", got, errb.String())
	}
}
