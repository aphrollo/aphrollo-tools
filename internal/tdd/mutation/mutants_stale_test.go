package mutation

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// errStub is the failure a stand-in clean reports, so the test is about what
// the purge does with a failure rather than about which failure it was.
var errStub = errors.New("cargo clean is not available in this test")

// A shard's persistent build dir keeps whatever the LAST mutant of the
// previous run built. cargo judges freshness by mtime, and the source tree
// cargo-mutants copies for the next run carries the ORIGINAL mtimes, which are
// older than those artifacts — so the baseline links the previous run's mutant
// and reports its behaviour as the tree's own. Measured on a real run: the
// shard's test binaries were dated an hour NEWER than the source they were
// told to test, the build step said `Finished 'test' profile ... in 0.74s`
// having rebuilt nothing, and the baseline then failed with three behavioural
// deviations at once, one of which the current source cannot produce.
//
// The rule these tests pin: a MUTATED package's artifacts are never safe to
// reuse, whatever the source says, because what survives belongs to a mutant.
// A DEPENDENCY's artifacts are always safe — nothing mutates them — and they
// are the whole value of keeping the directory at all.

func TestPurgeMutatedArtifacts_RemovesTheMutatedPackagesAndKeepsTheDependencies(t *testing.T) {
	target := t.TempDir()
	dep := filepath.Join(target, "debug", "deps", "libserde-1a2b.rlib")
	writeArtifact(t, dep)

	var gotTarget string
	var gotPkgs []string
	t.Cleanup(setCargoCleanForTest(func(_, target string, pkgs []string) error {
		gotTarget, gotPkgs = target, pkgs
		return nil
	}))

	purgeMutatedArtifacts(t.TempDir(), target, []string{"forge_powertrain", "sim"}, io.Discard)

	if want := []string{"forge_powertrain", "sim"}; !reflect.DeepEqual(gotPkgs, want) {
		t.Errorf("cleaned %v, want exactly the mutated packages %v — a package left behind is a "+
			"mutant's binary the next baseline will link and report as the tree's own", gotPkgs, want)
	}
	if gotTarget != target {
		t.Errorf("cleaned in %q, want the shard's own build dir %q", gotTarget, target)
	}
	if _, err := os.Stat(dep); err != nil {
		t.Errorf("the dependency artifact is gone (%v), want it kept — nothing mutates a dependency, "+
			"and those artifacts are the whole reason the directory persists", err)
	}
}

func TestPurgeMutatedArtifacts_RemovesTheWholeDirWhenNoPackageIsNamed(t *testing.T) {
	target := t.TempDir()
	writeArtifact(t, filepath.Join(target, "debug", "deps", "libserde-1a2b.rlib"))

	t.Cleanup(setCargoCleanForTest(func(_, _ string, _ []string) error {
		t.Error("asked cargo to clean named packages, want the whole dir removed: a run that names " +
			"no package mutates the whole workspace, so nothing in the dir is provably safe")
		return nil
	}))

	purgeMutatedArtifacts(t.TempDir(), target, nil, io.Discard)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the build dir survived (%v), want it removed whole", err)
	}
}

func TestPurgeMutatedArtifacts_RemovesTheWholeDirWhenCargoCannotClean(t *testing.T) {
	target := t.TempDir()
	writeArtifact(t, filepath.Join(target, "debug", "deps", "libserde-1a2b.rlib"))

	t.Cleanup(setCargoCleanForTest(func(_, _ string, _ []string) error {
		return errStub
	}))

	purgeMutatedArtifacts(t.TempDir(), target, []string{"forge_powertrain"}, io.Discard)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the build dir survived a failed clean (%v), want it removed whole — a clean that "+
			"did not happen leaves a mutant's artifacts exactly where the next baseline will link them", err)
	}
}

func TestMeasure_PurgesEachShardsBuildDirBeforeThatShardRuns(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	// The dirs a PREVIOUS run left, each holding what its last mutant built.
	for shard := range 3 {
		writeArtifact(t, filepath.Join(mutantsShardTargetDir(root, shard), "debug", "deps", "engine-1a2b.exe"))
	}

	var mu sync.Mutex
	var order []string
	t.Cleanup(setCargoCleanForTest(func(_, target string, pkgs []string) error {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "clean "+filepath.Base(target)+" "+strings.Join(pkgs, ","))
		return nil
	}))
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		mu.Lock()
		order = append(order, "run "+filepath.Base(envValueOf(c.Env, "CARGO_TARGET_DIR")))
		mu.Unlock()
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	for shard := range 3 {
		name := "target-" + strconv.Itoa(shard)
		clean, run := indexOfPrefix(order, "clean "+name+" "), indexOfPrefix(order, "run "+name)
		if clean < 0 {
			t.Errorf("shard %d built in %s without it being cleaned first: %v — the artifacts there "+
				"belong to the previous run's last mutant", shard, name, order)
			continue
		}
		if run >= 0 && clean > run {
			t.Errorf("shard %d ran before its build dir was cleaned: %v", shard, order)
		}
		if got := order[clean]; !strings.HasSuffix(got, " a") {
			t.Errorf("cleaned %q, want the mutated package `a` named — the packages the run mutates "+
				"are exactly the ones whose artifacts cannot be reused", got)
		}
	}
}

// indexOfPrefix is where a step with this prefix first appears, or -1.
func indexOfPrefix(steps []string, prefix string) int {
	for i, s := range steps {
		if strings.HasPrefix(s, prefix) {
			return i
		}
	}
	return -1
}

// writeArtifact puts one file where a cargo build dir would hold it.
func writeArtifact(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("artifact"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// `cargo clean exited 101` names the code and nothing else, so a locked file,
// an unmatched package spec and a broken toolchain all read identically —
// and the run's output went to io.Discard, so the one place the reason
// existed was thrown away. This is the line a session actually sees when a
// surgical clean falls back to removing the whole build dir, and it has to
// carry cargo's own words.
func TestCargoCleanPackages_FoldsCargosOwnMessageIntoTheError(t *testing.T) {
	restore := setCleanExecForTest(func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		io.WriteString(log, "     Removing D:/Projects/.mutants/borld/target-0/debug\n"+
			"error: failed to remove file `target-0/debug/deps/forge.pdb`\n"+
			"Caused by: Access is denied. (os error 5)\n")
		return 101, nil
	})
	defer restore()

	err := cargoCleanPackages(t.TempDir(), t.TempDir(), []string{"forge", "forge_solver"})

	if err == nil {
		t.Fatal("a non-zero clean must be an error")
	}
	for _, want := range []string{"101", "Access is denied. (os error 5)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q — the exit code alone tells a session nothing "+
				"about which failure it hit", err.Error(), want)
		}
	}
}

// The message is one line inside a run that already prints a lot, so what it
// quotes is bounded: a clean that fails on every file in a build dir must not
// paste that dir into the log.
func TestCargoCleanPackages_BoundsWhatItQuotes(t *testing.T) {
	restore := setCleanExecForTest(func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		for i := 0; i < 500; i++ {
			io.WriteString(log, "error: failed to remove file number "+strconv.Itoa(i)+"\n")
		}
		return 101, nil
	})
	defer restore()

	err := cargoCleanPackages(t.TempDir(), t.TempDir(), []string{"forge"})

	if err == nil {
		t.Fatal("a non-zero clean must be an error")
	}
	if n := len(err.Error()); n > 600 {
		t.Errorf("error is %d bytes, want it bounded — a failing clean must not paste a build dir "+
			"into the run's log", n)
	}
	if !strings.Contains(err.Error(), "number 499") {
		t.Errorf("error = %q, want the LAST lines — cargo's own diagnosis is what it ends with", err.Error())
	}
}
