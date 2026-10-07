package mutation

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// covdepsFixture is a module root with the package a (one embedded file, a
// testdata file), a dependency dep inside the module, and the listing that
// names them.
func covdepsFixture(t *testing.T) (root, listing string) {
	t.Helper()
	root = t.TempDir()
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "a", "a_test.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "a", "embedded.txt"), "one")
	mustWrite(t, filepath.Join(root, "a", "testdata", "golden.txt"), "one")
	mustWrite(t, filepath.Join(root, "dep", "dep.go"), "package dep\n")
	listing = filepath.Join(root, "a") + "|a.go|a_test.go||embedded.txt||\n" +
		"\n" +
		filepath.Join(root, "dep") + "|dep.go||||\n"
	return root, listing
}

func TestHashDeps_FollowsWhatTheTestsReadBesidesTheirOwnFunctions(t *testing.T) {
	root, listing := covdepsFixture(t)
	base := hashDeps(root, "a", listing)
	for name, edit := range map[string]func(){
		"a dependency inside the module": func() { mustWrite(t, filepath.Join(root, "dep", "dep.go"), "package dep // edited\n") },
		"the package's testdata":         func() { mustWrite(t, filepath.Join(root, "a", "testdata", "golden.txt"), "two") },
		"an embedded file":               func() { mustWrite(t, filepath.Join(root, "a", "embedded.txt"), "two") },
	} {
		edit()
		if hashDeps(root, "a", listing) == base {
			t.Errorf("%s: an edit did not change the hash", name)
		}
		base = hashDeps(root, "a", listing)
	}
	// The package's own Go files are told apart by function, not here.
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n\nfunc F() {}\n")
	mustWrite(t, filepath.Join(root, "a", "a_test.go"), "package a\n\nfunc G() {}\n")
	if hashDeps(root, "a", listing) != base {
		t.Error("an edit of the package's own Go files changed the dependency hash")
	}
}

func TestHashDeps_ListingOrderAndModuleCacheDependencies(t *testing.T) {
	root, listing := covdepsFixture(t)
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(listing, "\n\n", "\n")), "\n")
	slices.Reverse(lines)
	if hashDeps(root, "a", listing) != hashDeps(root, "a", strings.Join(lines, "\n")) {
		t.Error("the order of the listing changed the hash")
	}
	cached := filepath.Join(t.TempDir(), "pkg", "mod", "example.com", "x@v1.0.0")
	mustWrite(t, filepath.Join(cached, "x.go"), "package x\n")
	line := cached + "|x.go||||\n"
	first := hashDeps(root, "a", listing+line)
	mustWrite(t, filepath.Join(cached, "x.go"), "package x // edited\n")
	if hashDeps(root, "a", listing+line) != first {
		t.Error("a module-cache dependency was read; its directory names its version")
	}
	if hashDeps(root, "a", "") == "" {
		t.Error("an empty listing hashed to nothing")
	}
}

// A dependency change does not drop the store: its entries still pick the
// tests, but the selection is not exact, so a survivor goes on to the whole
// package.
func TestEnsureCoverage_ADependencyChangeKeepsTheEntriesButMakesTheMapInexact(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	var version atomic.Int32
	t.Cleanup(setGoListForTest(func(context.Context, string, string) (string, error) {
		mustWrite(t, filepath.Join(root, "dep", "dep.go"), "package dep // v"+string(rune('0'+version.Load()))+"\n")
		return filepath.Join(root, "dep") + "|dep.go||||\n", nil
	}))
	first := covbuildAsk(t, root, 1, 4)
	if first.Map.Inexact {
		t.Fatal("a map measured now is inexact")
	}
	version.Store(1)
	// Another function's tests are the candidates now, so Test_A is not remeasured.
	second := covbuildAsk(t, root, 1, 8)
	if second.Kept != 1 || second.Measured != 1 {
		t.Fatalf("second = %+v, want Test_A kept and Test_B measured, the store not dropped", second)
	}
	if !second.Map.Inexact {
		t.Fatal("Test_A was measured under other dependencies: the map must be inexact")
	}
	if sel := selectTests(&second.Map, nil, nil, "p.go", 4); sel.Exact || !slices.Equal(sel.Names, []string{"Test_A"}) {
		t.Fatalf("selection = %+v, want Test_A and not exact", sel)
	}
	// Asked for the function Test_A reaches, it is measured again and exact.
	third := covbuildAsk(t, root, 1, 4)
	if third.Measured != 1 || third.Map.Inexact {
		t.Fatalf("third = %+v, want Test_A remeasured and the map exact again", third)
	}
}

func TestSelectTests_AnInexactMapNeverSaysExactNorUncovered(t *testing.T) {
	t.Parallel()
	m := mapFor("Kind", "TestKind_A")
	m.Inexact = true
	if sel := selectTests(m, nil, nil, "gate.go", 5); sel.Exact || sel.Uncovered || sel.Partial || !slices.Equal(sel.Names, []string{"TestKind_A"}) {
		t.Fatalf("selection = %+v", sel)
	}
	empty := mapFor("Kind")
	empty.Inexact = true
	if sel := selectTests(empty, nil, nil, "gate.go", 5); !sel.Whole || sel.Uncovered {
		t.Fatalf("a line no test ran, on an inexact map: %+v, want the whole package", sel)
	}
}

// The fill window is read when each fill test is about to start, so a window
// that ends mid-run stops the fill and not only a window already over.
func TestEnsureCoverage_TheFillStopsStartingTestsWhenItsWindowEnds(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var now atomic.Int64 // seconds past base
	prevNow := commitNowFn
	commitNowFn = func() time.Time { return base.Add(time.Duration(now.Load()) * time.Second) }
	t.Cleanup(func() { commitNowFn = prevNow })
	testMapExecFn = func(c context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		code, err := tc.exec(c, dir, env, argv, log)
		if prefixValue(argv, "-test.run=") != "" {
			now.Add(10) // each solo run takes ten seconds of the injected clock
		}
		return code, err
	}
	// Test_A is the targeted test; the window ends after 15 s, i.e. after the
	// second run, so Test_B starts and Test_C does not.
	req := covRequest{Dir: "internal/p", Mutants: []commitMutant{covbuildMutant(4)}, Workers: 1, FillBy: base.Add(15 * time.Second)}
	res, err := ensureCoverage(context.Background(), root, MutantsConfig{}, req, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Measured != 2 {
		t.Fatalf("measured %d, want the targeted test and one fill test", res.Measured)
	}
	_, solo := testRuns(tc)
	if solo != 2 {
		t.Fatalf("%d solo runs, want 2", solo)
	}
	if res.Unmeasured != 1 {
		t.Fatalf("unmeasured = %d, want Test_C only", res.Unmeasured)
	}
}
