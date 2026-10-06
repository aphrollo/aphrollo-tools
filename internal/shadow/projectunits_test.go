package shadow

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/store"
)

// pjMarkerRoot stands in for the edit hook's marker walk over a repository that
// holds a Python backend and a TypeScript frontend: the nearest pyproject.toml or
// package.json.
func pjMarkerRoot(file string) string {
	return findUpAny(filepath.Dir(file), "pyproject.toml", "package.json")
}

// pjBox is a lane's checkout of a repository with a Python backend, a TypeScript
// frontend and a Go service, wired as a World. No test tool runs: a run is the
// fact the hooks fold, so the scoring is proved without pytest or vitest.
func pjBox(t *testing.T) *shadowBox {
	t.Helper()
	b := newShadowBox(t)
	b.root = tree(t, ".git",
		"backend/pyproject.toml", "backend/app/service.py", "backend/tests/test_service.py",
		"frontend/package.json", "frontend/src/api.ts", "frontend/src/api.test.ts", "frontend/src/legacy.js",
		"backend-go/go.mod", "backend-go/pkg/p.go")
	b.world.ProjectRoot = pjMarkerRoot
	return b
}

// pjFoldRun folds the edits a run judged, then the run itself, as the hooks do, in
// the project at rel and with the command argv.
func pjFoldRun(b *shadowBox, rel string, argv []string, tree, job string, v kernel.Verdict, ids ...string) string {
	b.t.Helper()
	return b.foldRun(Fold{Root: filepath.Join(b.root, rel), Actor: "s1", Tree: tree, Job: job, EditIDs: ids, Argv: argv, Verdict: v})
}

func TestRedGreen_ACodeEditInAnUntestedPythonProjectIsAWouldBeBlock(t *testing.T) {
	b := pjBox(t)
	got := b.askRedGreen("backend/app/service.py")
	if got.Unit != "python:backend" || got.Trellis != "block" || got.Relation != TrellisStricter {
		t.Errorf("record = %+v, want a would-be block of unit python:backend", got)
	}
}

func TestRedGreen_APythonTestsRedRunOpensTheProjectsCodeEditsAndItsGreenCoversThem(t *testing.T) {
	b := pjBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("backend/tests/test_service.py"), At: t0}}
	if cause := pjFoldRun(b, "backend", []string{"python", "-m", "pytest"}, "a1", "j1", kernel.VerdictRed, "e1"); cause != "" {
		t.Fatalf("the fold of a pytest red failed: %q", cause)
	}
	if got := b.unit("python:backend"); got.LastReal != kernel.VerdictRed {
		t.Fatalf("unit = %+v, want the red of the pytest run as its red fact", got)
	}
	if got := b.askRedGreen("backend/app/service.py"); got.Trellis != "allow" || got.Relation != Agree {
		t.Errorf("record = %+v, want the code edit allowed while the project's red is open", got)
	}
	// The code edit that follows, and a green run that started after it, cover the project.
	b.ledger = append(b.ledger, LedgerEdit{ID: "e2", File: b.file("backend/app/service.py"), At: t0.Add(time.Minute)})
	if cause := pjFoldRun(b, "backend", []string{"python", "-m", "pytest"}, "b2", "j2", kernel.VerdictGreen, "e1", "e2"); cause != "" {
		t.Fatalf("the fold of a pytest green failed: %q", cause)
	}
	_, err := b.store.RecordVerdict(b.ctx(), "b2", b.lane, store.Verdict{Runs: []store.RunVerdict{
		{Runner: "python", Unit: "backend|python -m pytest", Result: kernel.VerdictGreen, MS: 2000, At: t0.Add(time.Minute + 5*time.Second)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.askRedGreen("backend/app/service.py"); got.Trellis != "allow" || got.Relation != Agree {
		t.Errorf("record = %+v, want the covered project's code edit allowed", got)
	}
	// An edit newer than the green leaves the project uncovered again.
	b.ledger = append(b.ledger, LedgerEdit{ID: "e3", File: b.file("backend/app/service.py"), At: t0.Add(time.Hour)})
	if got := b.askRedGreen("backend/app/service.py"); got.Trellis != "block" {
		t.Errorf("record = %+v, want an edit newer than the green to leave the project uncovered", got)
	}
}

func TestFoldRun_AVitestRunStampsOnlyItsOwnProjectsUnit(t *testing.T) {
	b := pjBox(t)
	b.ledger = []LedgerEdit{
		{ID: "e1", File: b.file("frontend/src/api.test.ts"), At: t0},
		{ID: "e2", File: b.file("backend/app/service.py"), At: t0},
	}
	cause := b.foldRun(Fold{Root: filepath.Join(b.root, "frontend"), Actor: "s1", Tree: "a1", Job: "j1", EditIDs: []string{"e1", "e2"},
		Argv: []string{"npx", "vitest", "run"}, Verdict: kernel.VerdictRed})
	if cause != "" {
		t.Fatalf("FoldRun = %q", cause)
	}
	if got := b.unit("typescript:frontend"); got.LastReal != kernel.VerdictRed {
		t.Errorf("unit = %+v, want the vitest red stamped on the frontend", got)
	}
	if got := b.unit("python:backend"); got.LastReal != "" || got.Tree != "" {
		t.Errorf("unit = %+v, want the backend untouched by a run of the frontend", got)
	}
}

// A node project holds .ts tests beside .js code: one project is one unit, however
// its files are spelled, or a test edit would open no unit the code edit reads.
func TestUnitOf_AJavaScriptFileAndATypeScriptFileOfOneProjectShareTheUnit(t *testing.T) {
	b := pjBox(t)
	js, _ := UnitOf(b.file("frontend/src/legacy.js"), pjMarkerRoot)
	ts, _ := UnitOf(b.file("frontend/src/api.ts"), pjMarkerRoot)
	if js.ID != "typescript:frontend" || ts.ID != js.ID {
		t.Errorf("units = %q and %q, want one unit typescript:frontend", js.ID, ts.ID)
	}
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("frontend/src/api.test.ts"), At: t0}}
	pjFoldRun(b, "frontend", []string{"npx", "vitest", "run"}, "a1", "j1", kernel.VerdictRed, "e1")
	if got := b.askRedGreen("frontend/src/legacy.js"); got.Trellis != "allow" || got.Unit != "typescript:frontend" {
		t.Errorf("record = %+v, want the js code edit to read the ts test's open red", got)
	}
}

func TestCovered_AProjectRootUnitIsCoveredByAGreenRunOfItsProjectOnly(t *testing.T) {
	u := Unit{ID: "python:backend", Project: "backend", Kind: unitProjectRoot}
	unitOf := func(string) (Unit, bool) { return u, true }
	edits := []LedgerEdit{{File: "service.py", At: t0}}
	green := func(runUnit string) []store.RunVerdict {
		return []store.RunVerdict{runOf(kernel.VerdictGreen, runUnit, t0.Add(4*time.Second), 4000)}
	}
	for _, c := range []struct {
		name string
		runs []store.RunVerdict
		want bool
	}{
		{"a pytest of the project", green("backend|python -m pytest tests/test_service.py"), true},
		{"a run of another project", green("frontend|npx vitest run"), false},
		{"a red run", []store.RunVerdict{runOf(kernel.VerdictRed, "backend|pytest", t0.Add(4*time.Second), 4000)}, false},
	} {
		if got := Covered(u, c.runs, edits, unitOf); got != c.want {
			t.Errorf("%s: Covered = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAddsSymbol_ReadsAPythonAndATypeScriptEditsTextForANewFunctionOrPublicName(t *testing.T) {
	edit := func(old, now string) Payload {
		var p Payload
		p.ToolName = "Edit"
		p.ToolInput.OldString, p.ToolInput.NewString = old, now
		return p
	}
	cases := []struct {
		name string
		p    Payload
		file string
		want bool
	}{
		{"a new python def", edit("x = 1", "x = 1\n\ndef added():\n    return 2"), "a.py", true},
		{"a new private python def counts: it is a new function", edit("", "def _helper():\n    pass"), "a.py", true},
		{"a new method", edit("", "    def run(self):\n        pass"), "a.py", true},
		{"a python body edit", edit("return 1", "return 2"), "a.py", false},
		{"the same def with a new body", edit("def old():\n    return 1", "def old():\n    return 2"), "a.py", false},
		{"a new public python class", edit("", "class Service:\n    pass"), "a.py", true},
		{"a new private python class does not count", edit("", "class _Cache:\n    pass"), "a.py", false},
		{"a new ts function", edit("", "function added() {}"), "a.ts", true},
		{"a new exported const", edit("", "export const limit = 3"), "a.ts", true},
		{"a new unexported const does not count", edit("", "const limit = 3"), "a.ts", false},
		{"a new exported class in a tsx file", edit("", "export class Api {}"), "a.tsx", true},
		{"a new arrow function binding counts", edit("", "const add = (a: number) => a + 1"), "a.ts", true},
		{"a new exported interface", edit("", "export interface Opts { a: number }"), "a.ts", true},
		{"a ts body edit", edit("return 1", "return 2"), "a.ts", false},
		{"a new js function", edit("", "function added() {}"), "a.js", true},
	}
	for _, c := range cases {
		if got := AddsSymbol(c.p, c.file); got != c.want {
			t.Errorf("%s: AddsSymbol = %v, want %v", c.name, got, c.want)
		}
	}
}

// A fire of a project-root unit names its project's root, which the measure joins a
// commit proof to; a Go unit names its package instead.
func TestRedGreen_AProjectRootUnitsRecordNamesItsProjectRootAndAGoUnitsItsPackage(t *testing.T) {
	b := pjBox(t)
	py := b.askRedGreen("backend/app/service.py")
	if want := filepath.ToSlash(filepath.Join(b.root, "backend")); py.UnitRoot != want || py.UnitPkg != "" {
		t.Errorf("python record: unit root %q pkg %q, want root %q and no package", py.UnitRoot, py.UnitPkg, want)
	}
	if got := py.event(Source{Root: b.root}, b.lane).Detail["unit_root"]; got != py.UnitRoot {
		t.Errorf("event detail unit_root = %q, want %q", got, py.UnitRoot)
	}
	goRec := b.askRedGreen("backend-go/pkg/p.go")
	if goRec.UnitRoot != "" || goRec.UnitPkg != "pkg" {
		t.Errorf("go record: unit root %q pkg %q, want no root and package pkg", goRec.UnitRoot, goRec.UnitPkg)
	}
}

// The recorded payloads of a Python and a TypeScript edit are code writes, and read
// as ones that add a symbol.
func TestPayload_ARecordedPythonOrTypeScriptEditIsACodeWriteThatAddsASymbol(t *testing.T) {
	for _, c := range []struct{ file, target string }{
		{"pretooluse_edit_py.json", "/w/repo/backend/app/service.py"},
		{"pretooluse_write_ts.json", "/w/repo/frontend/src/api.ts"},
	} {
		p := recorded(t, c.file)
		targets := p.Targets()
		if len(targets) != 1 || filepath.ToSlash(targets[0]) != c.target || !hasWrites(targets) || FileClassOf(targets[0]) != kernel.ClassCode {
			t.Fatalf("%s: targets = %v, want the one code file %s", c.file, targets, c.target)
		}
		if !AddsSymbol(p, targets[0]) {
			t.Errorf("%s: AddsSymbol = false, want the added function or export read from the payload", c.file)
		}
	}
}

// A repository with go.mod and a Python or node manifest in one root: the project is
// the same, the language of the run is not. A green go test of the root covers no
// Python unit of it, and a pytest covers no Go package.
func TestCovered_ARunOfAnotherLanguageInASharedRootCoversNothing(t *testing.T) {
	py := Unit{ID: "python:.", Project: ".", Kind: unitProjectRoot}
	ts := Unit{ID: "typescript:.", Project: ".", Kind: unitProjectRoot}
	goPkg := Unit{ID: "pkg", Project: ".", Pkg: "pkg", Kind: unitGoPackage}
	edits := []LedgerEdit{{File: "f", At: t0}}
	for _, c := range []struct {
		name string
		u    Unit
		run  string
		want bool
	}{
		{"go test over a python unit", py, ".|go test ./...", false},
		{"pytest over a python unit", py, ".|python -m pytest", true},
		{"bare pytest over a python unit", py, ".|pytest -x", true},
		{"vitest over a python unit", py, ".|npx vitest run", false},
		{"vitest over a ts unit", ts, ".|npx vitest run", true},
		{"pytest over a ts unit", ts, ".|pytest", false},
		{"pytest over a go package", goPkg, ".|pytest", false},
		{"go test over a go package", goPkg, ".|go test ./...", true},
		{"a command naming no language covers the project", py, ".|make test", true},
	} {
		runs := []store.RunVerdict{runOf(kernel.VerdictGreen, c.run, t0.Add(4*time.Second), 4000)}
		if got := Covered(c.u, runs, edits, func(string) (Unit, bool) { return c.u, true }); got != c.want {
			t.Errorf("%s: Covered = %v, want %v", c.name, got, c.want)
		}
	}
}
