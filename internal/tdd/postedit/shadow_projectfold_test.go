package postedit

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
)

// kpjfoldHarvest folds the finished run of a project the way the hooks do: the
// edit of file is recorded and queued, the run's job is harvested, and the queue is
// flushed. It answers the lane record's unit of the project.
func kpjfoldHarvest(t *testing.T, linked, projectRel, fileRel string, runner []string, log string, exit int) kernel.Unit {
	t.Helper()
	// The record under test is not the hook's budget: a loaded box must not turn it into a drop.
	oldBudget := shadow.Budget
	shadow.Budget = time.Minute
	t.Cleanup(func() { shadow.Budget = oldBudget })
	root := filepath.Join(linked, filepath.FromSlash(projectRel))
	file := filepath.Join(linked, filepath.FromSlash(fileRel))
	if got := FindProjectRoot(file); got != root {
		t.Fatalf("FindProjectRoot(%s) = %q, want the project %q", fileRel, got, root)
	}
	editID := recordEdit(root, file)
	if editID == "" {
		t.Fatal("setup: the edit was not recorded in the ledger")
	}
	shadow.QueueEditFold(shadow.Source{Root: root, Actor: "s-pj"}, shadowWorld(), shadow.EditFold{Root: root, Actor: "s-pj", EditID: editID, File: file})
	saveDeferredJob(DeferredJob{
		Project: root, Phase: "run", Dir: root, PID: 99, Runner: runner,
		Started: time.Now(), Session: "s-pj", File: file, EditID: editID,
		FileHash: sourceIdentity(root, file), HeadSHA: headSHAFor(root),
	})
	job, ok := loadDeferredJob("s-pj", root)
	if !ok {
		t.Fatal("setup: the job did not load back")
	}
	mustWrite(t, job.Log, log)
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: exit, Seconds: 1, TreeKey: "tree-pj"})
	if _, ok := WaitDeferredEditJob(root); !ok {
		t.Fatal("the harvest found no job")
	}
	shadow.Flush()
	st, err := shadowWorld().Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rec, _, err := st.Load(ctx, "lane/x")
	if err != nil {
		t.Fatal(err)
	}
	return rec.Units[kpjfoldUnitID(t, linked, fileRel)]
}

// kpjfoldUnitID is the id of the unit the shadow names for a file of the lane.
func kpjfoldUnitID(t *testing.T, linked, fileRel string) string {
	t.Helper()
	u, ok := shadow.UnitOf(filepath.Join(linked, filepath.FromSlash(fileRel)), FindProjectRoot)
	if !ok {
		t.Fatalf("no unit for %s", fileRel)
	}
	return u.ID
}

// A pytest or vitest run of a project is folded into its lane's record as the
// run of the project's unit, and a red of it is that unit's red fact.
func TestHarvest_APytestRedRunIsTheRedFactOfThePythonProjectsUnit(t *testing.T) {
	shadow.Flush() // another test's queued run is not this one's
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	mustWrite(t, filepath.Join(linked, "backend", "pyproject.toml"), "[project]\nname = \"backend\"\n")
	mustWrite(t, filepath.Join(linked, "backend", "tests", "test_a.py"), "def test_a():\n    assert 1 == 2\n")

	u := kpjfoldHarvest(t, linked, "backend", "backend/tests/test_a.py", []string{"python", "-m", "pytest"},
		"FAILED tests/test_a.py::test_a - assert 1 == 2\n1 failed in 0.02s\n", 1)
	if u.LastReal != kernel.VerdictRed || u.LastRealTree != "tree-pj" || u.Phase != kernel.PhaseOpen {
		t.Errorf("unit = %+v, want the pytest red on tree-pj with the unit open", u)
	}
}

func TestHarvest_AVitestRedRunIsTheRedFactOfTheTypeScriptProjectsUnit(t *testing.T) {
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	mustWrite(t, filepath.Join(linked, "frontend", "package.json"), `{"name": "frontend", "devDependencies": {"vitest": "3.2.7"}}`)
	mustWrite(t, filepath.Join(linked, "frontend", "src", "api.test.ts"), "import { test, expect } from 'vitest'\ntest('a', () => expect(1).toBe(2))\n")

	u := kpjfoldHarvest(t, linked, "frontend", "frontend/src/api.test.ts", []string{"npx", "vitest", "run"},
		" FAIL  src/api.test.ts > a\nAssertionError: expected 1 to be 2\n Test Files  1 failed (1)\n", 1)
	if u.LastReal != kernel.VerdictRed || u.LastRealTree != "tree-pj" {
		t.Errorf("unit = %+v, want the vitest red on tree-pj", u)
	}
}

// kpjfoldRealRun runs a test tool in dir and answers what it printed and its exit
// code, for a run whose log is the tool's own.
func kpjfoldRealRun(t *testing.T, dir string, argv ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, out)
	}
	return string(out), code
}

// The same fold from what a real pytest prints: skipped when python has no pytest.
func TestHarvest_ARealPytestRedLogFoldsAsTheProjectsRed(t *testing.T) {
	if err := exec.Command("python", "-m", "pytest", "--version").Run(); err != nil {
		t.Skip("python with pytest is not installed") // skip-ok: the tool under test is pytest, which this box lacks
	}
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	backend := filepath.Join(linked, "backend")
	mustWrite(t, filepath.Join(backend, "pyproject.toml"), "[project]\nname = \"backend\"\n")
	mustWrite(t, filepath.Join(backend, "tests", "test_a.py"), "def test_a():\n    assert 1 == 2\n")
	log, code := kpjfoldRealRun(t, backend, "python", "-m", "pytest", "-p", "no:cacheprovider", "-q")
	if code == 0 {
		t.Fatalf("setup: the failing test passed:\n%s", log)
	}
	u := kpjfoldHarvest(t, linked, "backend", "backend/tests/test_a.py", []string{"python", "-m", "pytest"}, log, code)
	if u.LastReal != kernel.VerdictRed || u.LastRealTree != "tree-pj" {
		t.Errorf("unit = %+v, want the real pytest red on tree-pj", u)
	}
}

// The same from what a real vitest prints: skipped when the box has no vitest.
func TestHarvest_ARealVitestRedLogFoldsAsTheProjectsRed(t *testing.T) {
	vitest, err := exec.LookPath("vitest")
	if err != nil {
		t.Skip("vitest is not installed") // skip-ok: the tool under test is vitest, which this box lacks
	}
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	frontend := filepath.Join(linked, "frontend")
	mustWrite(t, filepath.Join(frontend, "package.json"), `{"name": "frontend", "type": "module"}`)
	mustWrite(t, filepath.Join(frontend, "src", "api.test.ts"), "import { test, expect } from 'vitest'\ntest('a', () => expect(1).toBe(2))\n")
	log, code := kpjfoldRealRun(t, frontend, vitest, "run")
	if code == 0 {
		t.Fatalf("setup: the failing test passed:\n%s", log)
	}
	u := kpjfoldHarvest(t, linked, "frontend", "frontend/src/api.test.ts", []string{"npx", "vitest", "run"}, log, code)
	if u.LastReal != kernel.VerdictRed || u.LastRealTree != "tree-pj" {
		t.Errorf("unit = %+v, want the real vitest red on tree-pj", u)
	}
}

// A run's shadow record names the language of its runner, so stats reads
// agreement per language.
func TestHarvest_ARunsShadowRecordNamesTheLanguageOfItsRunner(t *testing.T) {
	shadow.Flush()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, linked := primaryRepo(t)
	mustWrite(t, filepath.Join(linked, "frontend", "package.json"), `{"name": "frontend"}`)
	mustWrite(t, filepath.Join(linked, "frontend", "src", "api.test.ts"), "test('a', () => {})\n")
	kpjfoldHarvest(t, linked, "frontend", "frontend/src/api.test.ts", []string{"npx", "vitest", "run"},
		" FAIL  src/api.test.ts > a\nAssertionError: expected 1 to be 2\n", 1)
	var langs []string
	for _, e := range eventsOfKind(filepath.Join(linked, "frontend"), "shadow") {
		if e.Detail["rule"] == "run-verdict" {
			langs = append(langs, e.Detail["lang"])
		}
	}
	if len(langs) != 1 || langs[0] != "ts" {
		t.Errorf("run records name languages %v, want [ts]", langs)
	}
}
