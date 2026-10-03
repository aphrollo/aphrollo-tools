package suite

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// hubRepo is a module whose package directories each hold a Go file, with the
// import graph a hub package sits in: app and apigen are imported by server,
// server by cli, and other by nobody.
func hubRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"internal/app", "internal/server", "internal/server/apigen", "internal/cli", "internal/other"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(dir), "x.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stubGoWorkspaceGraph(t, map[string][]string{
		"internal/app":           nil,
		"internal/server/apigen": nil,
		"internal/server":        {"internal/app", "internal/server/apigen"},
		"internal/cli":           {"internal/server"},
		"internal/other":         nil,
	})
	return root
}

// mergeRunner is the runner the merge gate builds for files: the touched
// packages and their importers, with CI's flags and -race on all of them.
func mergeRunner(t *testing.T, root string, files []string) Runner {
	t.Helper()
	scoped, ok := narrowToStaged(Runner{Cmd: "go", Args: []string{"test", "./..."}}, root, files)
	if !ok {
		t.Fatal("setup: the files must narrow the runner")
	}
	args := append([]string{"test", "-race", "-count=1", "-shuffle=on"}, scoped.Args[1:]...)
	return Runner{Cmd: "go", Args: args}
}

// #1172: a lane touching the hub packages app and apigen has the whole fan of
// their importers in its merge run, and every one of them ran under -race.
// -race now covers what the lane changed and the importers run without it, in
// a second command with the same -count=1 -shuffle=on.
func TestSplitRaceRuns_RacesTheChangedPackagesAndRunsTheirImportersPlain(t *testing.T) {
	root := hubRepo(t)
	files := []string{"internal/app/app.go", "internal/server/apigen/gen.go"}
	full := mergeRunner(t, root, files)

	wantFull := []string{"test", "-race", "-count=1", "-shuffle=on",
		"./internal/app", "./internal/cli", "./internal/server", "./internal/server/apigen"}
	if !slices.Equal(full.Args, wantFull) {
		t.Fatalf("setup: the merge runner is %v, want %v", full.Args, wantFull)
	}

	runs := splitRaceRuns(full, root, root, files)

	if len(runs) != 2 {
		t.Fatalf("got %d runs, want the race run and the importers run: %+v", len(runs), runs)
	}
	wantRace := []string{"test", "-race", "-count=1", "-shuffle=on", "./internal/app", "./internal/server/apigen"}
	wantPlain := []string{"test", "-count=1", "-shuffle=on", "./internal/cli", "./internal/server"}
	if !slices.Equal(runs[0].Args, wantRace) {
		t.Errorf("race run = %v, want %v", runs[0].Args, wantRace)
	}
	if !slices.Equal(runs[1].Args, wantPlain) {
		t.Errorf("importers run = %v, want %v", runs[1].Args, wantPlain)
	}
	if runs[0].Cmd != "go" || runs[1].Cmd != "go" {
		t.Errorf("commands = %q, %q, want go for both", runs[0].Cmd, runs[1].Cmd)
	}
}

// Every package of the original run is in exactly one of the two: splitting
// must lose no ground.
func TestSplitRaceRuns_EveryPackageIsRunExactlyOnce(t *testing.T) {
	root := hubRepo(t)
	files := []string{"internal/app/app.go"}
	full := mergeRunner(t, root, files)

	runs := splitRaceRuns(full, root, root, files)
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2: %+v", len(runs), runs)
	}
	var got []string
	for _, r := range runs {
		for _, a := range r.Args {
			if len(a) > 2 && a[:2] == "./" {
				got = append(got, a)
			}
		}
	}
	slices.Sort(got)
	want := []string{"./internal/app", "./internal/cli", "./internal/server"}
	if !slices.Equal(got, want) {
		t.Fatalf("the two runs name %v together, want exactly the original %v", got, want)
	}
}

// A repo that wants -race everywhere says so, and keeps the one run it had.
func TestSplitRaceRuns_RaceScopeAllKeepsTheWholeRunUnderRace(t *testing.T) {
	root := hubRepo(t)
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte("[aphrollo]\nrace-scope = \"all\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []string{"internal/app/app.go"}
	full := mergeRunner(t, root, files)

	runs := splitRaceRuns(full, root, root, files)

	if len(runs) != 1 || !slices.Equal(runs[0].Args, full.Args) {
		t.Fatalf("race-scope = all: got %+v, want the single run %v", runs, full.Args)
	}
}

// Nothing to split: no -race on the line, nothing but the changed packages
// (which is also what a run narrowed over an unreadable graph holds), or a
// list this cannot read back.
func TestSplitRaceRuns_LeavesARunItCannotSplitAsItWas(t *testing.T) {
	root := hubRepo(t)
	hubFiles := []string{"internal/app/app.go"}

	plain := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./internal/app", "./internal/server"}}
	leafFiles := []string{"internal/other/o.go"}
	leaf := mergeRunner(t, root, leafFiles)
	whole := Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "./..."}}
	cargo := Runner{Cmd: "cargo", Args: []string{"test"}}

	for name, c := range map[string]struct {
		r     Runner
		files []string
	}{
		"no -race":                        {plain, hubFiles},
		"a leaf package with no importer": {leaf, leafFiles},
		"the whole-module fallback":       {whole, hubFiles},
		"not go":                          {cargo, hubFiles},
	} {
		runs := splitRaceRuns(c.r, root, root, c.files)
		if len(runs) != 1 || !slices.Equal(runs[0].Args, c.r.Args) || runs[0].Cmd != c.r.Cmd {
			t.Errorf("%s: got %+v, want the one run it was: %+v", name, runs, c.r)
		}
	}
}
