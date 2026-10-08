package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// test-cache = "edit" (or "commit") lets go's cache serve the post-edit suite;
// with it off the runner is the one the gate always ran.
func TestPostEdit_TestCacheSettingMarksTheRunnerCached(t *testing.T) {
	for _, c := range []struct {
		setting string
		want    bool
	}{{"", false}, {"off", false}, {"edit", true}, {"commit", true}} {
		t.Setenv("TRELLIS_DATA", t.TempDir())
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := mkProject(t, "go.mod")
		if c.setting != "" {
			body := "[aphrollo]\ntest-cache = \"" + c.setting + "\"\ntest-cache-impure = [\"./internal/git/...\"]\n"
			if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var seen Runner
		run := func(r Runner, _ string) SuiteResult {
			seen = r
			return SuiteResult{Passed: true, Output: "ok\nPASS"}
		}
		PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), run)
		if seen.Cmd != "go" {
			t.Fatalf("test-cache %q: the suite ran %q, want go", c.setting, seen.Cmd)
		}
		if seen.Cached != c.want {
			t.Errorf("test-cache %q: Cached = %v, want %v", c.setting, seen.Cached, c.want)
		}
		if c.want && len(seen.Impure) != 1 {
			t.Errorf("test-cache %q: Impure = %v, want the repo's list", c.setting, seen.Impure)
		}
	}
}

// A package go served from its cache is never shown as a fresh run: the label
// counts the tests and the cached packages apart.
func TestGreenLabel_NamesThePackagesServedFromTheCache(t *testing.T) {
	out := "--- PASS: TestA (0.00s)\nok  \texample.com/a\t(cached)\n--- PASS: TestB (0.00s)\nok  \texample.com/b\t0.150s\n"
	if got, want := greenLabel(Green, out, 1200*time.Millisecond), "green (2 passed (1 package cached), 1.2s)"; got != want {
		t.Fatalf("greenLabel = %q, want %q", got, want)
	}
	two := out + "ok  \texample.com/c\t(cached)\n"
	if got, want := greenLabel(Green, two, 1200*time.Millisecond), "green (2 passed (2 packages cached), 1.2s)"; got != want {
		t.Fatalf("greenLabel = %q, want %q", got, want)
	}
	fresh := "--- PASS: TestB (0.00s)\nok  \texample.com/b\t0.150s\n"
	if got := greenLabel(Green, fresh, 1200*time.Millisecond); strings.Contains(got, "cached") {
		t.Fatalf("a run with nothing cached says %q", got)
	}
}

// The unconstrained-green line states its count the same way: tests passed,
// and how many packages of them go served from its cache.
func TestUnconstrainedLine_NamesThePackagesServedFromTheCache(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./x"}}
	if got := unconstrainedLine(r, "/r", 4, 1, time.Second); !strings.Contains(got, "(4 passed (1 package cached); no test changed") {
		t.Fatalf("line = %q", got)
	}
	if got := unconstrainedLine(r, "/r", 4, 0, time.Second); !strings.Contains(got, "(4 passed; no test changed") {
		t.Fatalf("a run with nothing cached says %q", got)
	}
}

// A widened run (the rung after a narrowed run selected nothing) is the same
// run with more packages, so it is served from the cache the same way.
func TestGoWideningSteps_KeepTheCacheMarkOfTheRunTheyWiden(t *testing.T) {
	defer SetGoTestReachForTest(func(root, dir string) ([]string, error) { return []string{"b"}, nil })()

	r := Runner{Cmd: "go", Args: []string{"test", "./a"}, Dir: "d", Cached: true, Impure: []string{"./x"}}
	steps := goWideningSteps(r, "a/a.go", t.TempDir())
	if len(steps) != 1 {
		t.Fatalf("steps = %+v, want one rung", steps)
	}
	if !steps[0].Cached || len(steps[0].Impure) != 1 {
		t.Fatalf("rung = %+v, want Cached with the impure list kept", steps[0])
	}
}

// The detached run phase starts `go test` from an argv, so the mark does not
// travel with it: a cached run whose list holds an impure package is made
// whole and uncached there, where it cannot be split into two commands.
func TestPhaseArgv_ACachedRunWithAnImpurePackageRunsUncachedInTheDeferredPhase(t *testing.T) {
	impure := []string{"./internal/git"}
	cases := []struct {
		name string
		r    Runner
		want bool
	}{
		{"cached, nothing impure in the list", Runner{Cmd: "go", Args: []string{"test", "./a"}, Cached: true, Impure: impure}, false},
		{"cached, an impure package in the list", Runner{Cmd: "go", Args: []string{"test", "./a", "./internal/git"}, Cached: true, Impure: impure}, true},
		{"cached, the whole module", Runner{Cmd: "go", Args: []string{"test", "./..."}, Cached: true, Impure: impure}, true},
		{"not marked (key off): measured like every gate run, #421", Runner{Cmd: "go", Args: []string{"test", "./a", "./internal/git"}}, true},
		{"not marked, -count=1 already named", Runner{Cmd: "go", Args: []string{"test", "-count=1", "./a"}}, true},
	}
	for _, c := range cases {
		got := strings.Contains(strings.Join(phaseArgv(c.r, "run"), " "), "-count=1")
		if n := strings.Count(strings.Join(phaseArgv(c.r, "run"), " "), "-count=1"); n > 1 {
			t.Errorf("%s: -count=1 named %d times", c.name, n)
		}
		if got != c.want {
			t.Errorf("%s: -count=1 present = %v, want %v (argv %v)", c.name, got, c.want, phaseArgv(c.r, "run"))
		}
	}
}
