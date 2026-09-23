package tdd

import (
	"strings"
	"testing"
	"time"
)

// Issue #769: a post-edit line named one checkout as its run root and nothing
// a reader could fetch afterwards said where the run had actually built. A
// cargo run executes in its workspace directory, not in the member crate the
// record is keyed on, so the root alone cannot answer "which tree did this
// test". The directory the run executed in travels with its result from the
// runner that started it into the record `aphrollo gate output` serves.

func TestRunSuite_ResultNamesTheDirectoryTheRunExecutedIn(t *testing.T) {
	root := mkProject(t, "go.mod")
	ws := t.TempDir()

	res := RunSuite(time.Minute)(Runner{Cmd: "go", Args: []string{"env", "GOROOT"}, Dir: ws}, root)

	if res.Dir != ws {
		t.Fatalf("the result names %q as the run's directory, want the runner's own %q", res.Dir, ws)
	}
}

func TestPhaseSuiteResult_NamesTheDirectoryTheJobRanIn(t *testing.T) {
	ws := t.TempDir()
	j := DeferredJob{Project: t.TempDir(), Dir: ws, Phase: "run"}

	res := phaseSuiteResult(j, PhaseOutcome{})

	if res.Dir != ws {
		t.Fatalf("a deferred phase's result names %q as the run's directory, want the job's %q", res.Dir, ws)
	}
}

func TestRetainedSuiteOutput_NamesTheDirectoryTheRunExecutedIn(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	ws := t.TempDir()

	retainSuiteOutput("postedit", root, "cargo nextest run -p forge --lib", "green", SuiteResult{Output: "ok\n", Dir: ws})

	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput: %v", err)
	}
	if !strings.Contains(got, "\ndir: "+ws+"\n") {
		t.Fatalf("the retained record must name the directory the run executed in (%s):\n%s", ws, firstBytes(got, 400))
	}
}
