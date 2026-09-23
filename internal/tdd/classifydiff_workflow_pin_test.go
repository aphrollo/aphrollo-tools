package tdd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pipelineJobBlock isolates one top-level job of pipeline.yml, from its
// "  <name>:" key to the next top-level job key or EOF.
func pipelineJobBlock(t *testing.T, wf, name string) string {
	t.Helper()
	lines := strings.Split(wf, "\n")
	start := -1
	for i, line := range lines {
		if start < 0 {
			if line == "  "+name+":" {
				start = i
			}
			continue
		}
		if jobKeyLineRe.MatchString(line) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("no %q job in pipeline.yml, so this test proves nothing", name)
	}
	return strings.Join(lines[start:], "\n")
}

var changesOutputRe = regexp.MustCompile(`needs\.changes\.outputs\.([a-z]+)`)

// changesOutputsRead lists, sorted and distinct, which `changes` outputs a
// job's steps gate on.
func changesOutputsRead(job string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range changesOutputRe.FindAllStringSubmatch(job, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// The class comes from the gate's own classifier, built from this PR, and
// no path regex in the workflow second-guesses it. A regex here is exactly
// the sibling copy that drifted before: it could not see a //go:embed, so a
// package-local markdown file had to be treated as code by directory alone.
func TestPipeline_ChangesJobClassifiesWithTheGatesOwnEntry(t *testing.T) {
	t.Parallel()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "changes")
	if !strings.Contains(job, "go build -trimpath -buildvcs=false -o bin/aphrollo ./cmd/aphrollo") {
		t.Error("the changes job does not build the CLI from this PR, so the class would come from whatever binary the runner happens to carry")
	}
	if !strings.Contains(job, `./bin/aphrollo gate classify-diff "$BASE_SHA" HEAD`) {
		t.Error("the changes job does not take its class from `gate classify-diff`")
	}
	if strings.Contains(job, "grep -qvE") {
		t.Error("the changes job still decides docs-only with a path regex beside the classifier")
	}
}

// A build that fails must not fail the changes job: every job needing it
// would be skipped, and a skipped required check reads as a pass.
func TestPipeline_ChangesJobBuildFailureStillRunsEverything(t *testing.T) {
	t.Parallel()
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "changes")
	i := strings.Index(job, "go build -trimpath -buildvcs=false -o bin/aphrollo ./cmd/aphrollo")
	if i < 0 {
		t.Fatal("no CLI build in the changes job, so this test proves nothing")
	}
	step := job[:i]
	step = step[strings.LastIndex(step, "      - "):]
	if !strings.Contains(step, "continue-on-error: true") {
		t.Errorf("the changes job's CLI build is not continue-on-error, so a PR that does not compile skips every check:\n%s", step)
	}
}

// Which output each job keys on IS the fast-path table: a job reading the
// wrong one runs on a class it should not, or skips one it must cover.
func TestPipeline_EachJobKeysOnTheOutputItsClassNeeds(t *testing.T) {
	t.Parallel()
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	want := map[string][]string{
		"docs-check":      {"docs"},
		"test":            {"code", "refactor"},
		"gate-env":        {"code"},
		"lint":            {"lint"},
		"benchmarks":      {"bench"},
		"mutants-verdict": {"code"},
		"scan":            {"code"},
		"build":           {"code"},
		"workflow-pins":   {"workflow"},
	}
	for job, outs := range want {
		got := changesOutputsRead(pipelineJobBlock(t, wf, job))
		if strings.Join(got, ",") != strings.Join(outs, ",") {
			t.Errorf("job %q gates on changes outputs %v, want %v", job, got, outs)
		}
	}
}

// Every test that reads this repo's own .github files is what a
// workflow-only change can break, so the workflow-pins job must select every
// one of them. A new pin test the job's -run pattern or package list misses
// fails here, instead of passing a workflow change it was written to catch.
func TestPipeline_WorkflowPinsJobSelectsEveryWorkflowReadingTest(t *testing.T) {
	t.Parallel()
	root := repoRootForTest(t)
	job := pipelineJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"), "workflow-pins")
	m := regexp.MustCompile(`go test -count=1 -run '([^']+)' ((?:\./[a-z/]+ ?)+)`).FindStringSubmatch(job)
	if m == nil {
		t.Fatal("no `go test -count=1 -run '<pattern>' <packages>` line in workflow-pins, so this test proves nothing")
	}
	runRe := regexp.MustCompile(m[1])
	pkgs := map[string]bool{}
	for p := range strings.FieldsSeq(m[2]) {
		pkgs[strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/")] = true
	}

	files, err := git(root, "ls-files", "*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	readsRepo := regexp.MustCompile(`repoFile\(|repoRootForTest\(|findModuleRoot\(|RepoRoot\("\."\)`)
	found := 0
	for f := range strings.FieldsSeq(files) {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), `".github"`) || !readsRepo.Match(src) {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			found++
			if !runRe.MatchString(fn.Name.Name) {
				t.Errorf("%s reads this repo's .github files but workflow-pins' -run pattern %q does not select it", fn.Name.Name, m[1])
			}
			if !pkgs[path.Dir(f)] {
				t.Errorf("%s lives in %s, which workflow-pins does not test", fn.Name.Name, path.Dir(f))
			}
		}
	}
	if found == 0 {
		t.Fatal("found no test reading this repo's .github files, so this test proves nothing")
	}
}
