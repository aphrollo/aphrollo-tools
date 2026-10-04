package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A multi-root repo keeps its Go module in a subdirectory (fanpyp:
// backend-go/go.mod). The rest of the commit gate treats that directory as the
// Go root; the commit-time mutation stage has to as well, and has to run `go
// test` from it, with the package named relative to it.

// nestedCommitStage is commitStage with the module one directory down, the
// change of commitGateSource staged.
func nestedCommitStage(t *testing.T, config string) (cfgDir, root string) {
	t.Helper()
	cfgDir = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	t.Cleanup(SetCommitHeadroomForTest(func(string, time.Duration) string { return "" }))
	root = makeGoRepo(t)
	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "backend-go/go.mod", "module github.com/x/gate\n\ngo 1.22\n")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n"+config)
	write(t, root, "backend-go/gate/gate.go", commitBaseSource)
	write(t, root, "backend-go/gate/gate_test.go", "package gate\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "backend-go/gate/gate.go", commitGateSource)
	gitDo(t, root, "add", "backend-go/gate/gate.go")
	return cfgDir, root
}

// goRun is where one `go test` was started and what it was told to test.
type goRun struct {
	dir  string
	args []string
}

func recordGoRuns(t *testing.T) func() []goRun {
	t.Helper()
	var mu sync.Mutex
	var runs []goRun
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, dir string, _ []string, argv []string, log io.Writer) (int, error) {
		mu.Lock()
		runs = append(runs, goRun{dir: dir, args: append([]string(nil), argv...)})
		mu.Unlock()
		return 0, ctx.Err()
	}
	t.Cleanup(func() { resolveExecFn = prev })
	return func() []goRun {
		mu.Lock()
		defer mu.Unlock()
		return append([]goRun(nil), runs...)
	}
}

func TestMutantsAtCommitStage_AModuleInASubdirectoryIsMeasuredFromThatDirectory(t *testing.T) {
	cfgDir, root := nestedCommitStage(t, "")
	runs := recordGoRuns(t)

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if res.Blocked {
		t.Fatalf("a report-only run refused the commit: %q", res.Message)
	}
	if strings.Contains(stderr, "not a Go module") {
		t.Fatalf("stderr = %q, want the module in backend-go measured, not skipped", stderr)
	}
	got := runs()
	if len(got) != 2 {
		t.Fatalf("go test ran %d times, want one whole-package run for each of the two mutants:\n%s", len(got), stderr)
	}
	for _, r := range got {
		if filepath.Base(r.dir) != "backend-go" {
			t.Errorf("go test started in %q, want the backend-go directory", r.dir)
		}
		if r.args[len(r.args)-1] != "./gate" {
			t.Errorf("go test args = %v, want the package ./gate relative to the module", r.args)
		}
	}
	for _, want := range []string{"backend-go/gate/gate.go:4:7: CONDITIONALS_BOUNDARY", "REPORT ONLY"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-reported:tested=2,caught=0,unviable=0,missed=2") {
		t.Errorf("gate.log = %q, want the survivors counted", log)
	}
}

// A survivor is admitted by the repo-relative path the commit names.
func TestMutantsAtCommitStage_ASubdirectoryModuleSurvivorIsAcceptedByItsRepoPath(t *testing.T) {
	_, root := nestedCommitStage(t, "mutation-accept = [\n"+
		"  \"backend-go/gate/gate.go:4:7 CONDITIONALS_BOUNDARY # kind=equivalent: signed off\",\n"+
		"  \"backend-go/gate/gate.go:4:7 CONDITIONALS_NEGATION # kind=equivalent: signed off\",\n]\n")
	recordGoRuns(t)
	stderr := captureStderr(t, func() { mutantsAtCommitStage("precommit", root) })
	if strings.Contains(stderr, "REPORT ONLY") || !strings.Contains(stderr, "2 accepted") {
		t.Errorf("stderr = %q, want both survivors admitted by their backend-go paths", stderr)
	}
}

// A commit that stages no file of a Go module has nothing to measure, and
// says so without calling the repo a non-module.
func TestMutantsAtCommitStage_AStagedFileOutsideEveryModuleStandsDown(t *testing.T) {
	cfgDir, root := nestedCommitStage(t, "")
	gitDo(t, root, "reset", "-q", "backend-go/gate/gate.go")
	write(t, root, "docs/note.go", "package docs\n\nfunc F(n int) bool { return n > 1 }\n")
	gitDo(t, root, "add", "docs/note.go")
	s := recordGoRuns(t)

	res := mutantsAtCommitStage("precommit", root)

	if res.Blocked || len(s()) != 0 {
		t.Fatalf("stage = %+v after %d runs, want a pass that ran nothing", res, len(s()))
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-skipped:not-go") {
		t.Errorf("gate.log = %q, want the stand-down counted", log)
	}
}

// The test-map verb lists and builds from the module's own directory, found
// among the repo's tracked go.mod files, and names the packages the hook gives
// it relative to that directory.
func TestRunMutantsTestMap_AModuleInASubdirectoryIsListedFromThatDirectory(t *testing.T) {
	_, root := nestedCommitStage(t, "")
	var listed []string
	prev := goTestedPackagesFn
	goTestedPackagesFn = func(_ context.Context, dir string) (string, error) {
		listed = append(listed, filepath.Base(dir))
		return "", nil
	}
	t.Cleanup(func() { goTestedPackagesFn = prev })
	var out, errOut strings.Builder

	code := RunMutantsTestMap(root, nil, &out, &errOut)

	if code != 0 || len(listed) != 1 || listed[0] != "backend-go" {
		t.Fatalf("exit %d, packages listed from %v, stderr %q, want one listing from backend-go", code, listed, errOut.String())
	}
	if !strings.Contains(out.String(), "test maps: in backend-go,") {
		t.Errorf("stdout = %q, want the module named in the counts", out.String())
	}
}

func TestNamedUnder_TakesTheDirectoriesOfOneModuleRelativeToIt(t *testing.T) {
	t.Parallel()
	dirs := []string{"backend-go/gate", "backend-go", "frontend/src", "backend-gox/a"}
	if got := namedUnder("backend-go/", dirs); !slices.Equal(got, []string{"gate", "."}) {
		t.Errorf("under backend-go/ = %v, want [gate .]", got)
	}
	if got := namedUnder("", dirs); !slices.Equal(got, dirs) {
		t.Errorf("under the repo root = %v, want all of them", got)
	}
}

// The edit hook measures a file of the subdirectory module from that module.
func TestEditStage_AFileOfASubdirectoryModuleIsMeasuredFromTheModule(t *testing.T) {
	_, root := nestedCommitStage(t, "")
	runs := recordGoRuns(t)

	stderr := captureStderr(t, func() { editStage(root, "backend-go/gate/gate.go") })

	got := runs()
	if len(got) != 2 || filepath.Base(got[0].dir) != "backend-go" {
		t.Fatalf("go test runs %+v, want two, started in backend-go:\n%s", got, stderr)
	}
}
