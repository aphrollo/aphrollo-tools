package mutation

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #957: the pre-PR check measured PR #955 as 18 caught; the merge gate
// refused the same diff for one mutant UNRESOLVED. Both run MeasureLane, so
// neither the base, the mutant set nor the resolve step's code differed: the
// lane's gremlins report shows the mutant LIVED with its own package's tests,
// and settling it by the tests of the packages that reach it was credited as
// a kill in the lane and cut off in the merge checkout. By hand every one of
// those packages stayed green under it: a failure the mutant did not cause
// was taken for its kill, and a failure of that kind depends on the checkout
// the run happens in, not on the diff. A kill now stands only when the failed
// packages pass without the mutant.

// inconclusiveKind is gremlins' survivor on Kind's case condition, judged by
// its own package's tests while another package's tests reach it.
func inconclusiveKind() MutantOutcome {
	m := caseBoundary()
	m.Status = gremlinsScopeUnknown
	return m
}

// moduleWithAReachingPackage is gate beside a package whose tests reach it.
// otherTest is that package's test body.
func moduleWithAReachingPackage(t *testing.T, otherTest string) string {
	t.Helper()
	root := gateKindModule(t, "\t_ = Kind(100)\n")
	write(t, root, "go.mod", "module m\n\ngo 1.21\n")
	write(t, root, filepath.FromSlash("other/other.go"),
		"package other\n\nimport \"m/gate\"\n\nfunc Size(n int) string { return gate.Kind(n) }\n")
	write(t, root, filepath.FromSlash("other/other_test.go"),
		"package other\n\nimport \"testing\"\n\nfunc TestSize(t *testing.T) {\n"+otherTest+"}\n")
	return root
}

// TestResolveGapMutants_AFailureWithoutTheMutantIsNoKill is the RED of
// issue #957: the only package reaching the new line has a test that fails
// whatever the code. Its failure under the mutant is not the mutant's kill,
// and the mutant must stay refused, as the merge gate refused it, rather
// than pass the pre-PR check as caught.
func TestResolveGapMutants_AFailureWithoutTheMutantIsNoKill(t *testing.T) {
	root := moduleWithAReachingPackage(t, "\tt.Fatal(\"fails on this box whatever the code\")\n")

	got := resolveOneForTest(t, root, inconclusiveKind())

	if got.Status == "caught" {
		t.Fatalf("status = caught (%s), want it refused: other's test fails without the mutant too", got.Note)
	}
	if !strings.HasPrefix(got.Note, "UNRESOLVED:") || !strings.Contains(got.Note, "m/other fail without the mutant") {
		t.Fatalf("note = %q, want UNRESOLVED saying m/other's tests fail without the mutant too", got.Note)
	}
}

// TestResolveGapMutants_AKillByAnotherPackageStillCounts pins the other
// side: a reaching package whose test passes on the code and fails on the
// mutant killed it, and the confirmation run does not take that away.
func TestResolveGapMutants_AKillByAnotherPackageStillCounts(t *testing.T) {
	root := moduleWithAReachingPackage(t, "\tif Size(10) != \"small\" {\n\t\tt.Fatal(\"wrong size\")\n\t}\n")

	got := resolveOneForTest(t, root, inconclusiveKind())

	if got.Status != "caught" || !strings.Contains(got.Note, "m/other") {
		t.Fatalf("got %q (%s), want caught by m/other", got.Status, got.Note)
	}
}

// fakeResolveExec answers the mutant's run (the one carrying -overlay) with
// a failure of m/other, and the run without it with unmutated.
func fakeResolveExec(t *testing.T, unmutated func(ctx context.Context) (int, error)) {
	t.Helper()
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, _ string, _ []string, argv []string, log io.Writer) (int, error) {
		for _, a := range argv {
			if a == "-overlay" {
				_, _ = io.Copy(log, bytes.NewBufferString("--- FAIL: TestSize\nFAIL\tm/other\t0.01s\n"))
				return 1, nil
			}
		}
		return unmutated(ctx)
	}
	t.Cleanup(func() { resolveExecFn = prev })
}

// TestResolveGapMutants_AConfirmationRunCutOffIsUnresolved: the run without
// the mutant that would show the failure is its own did not finish, so
// nothing shows it is a kill.
func TestResolveGapMutants_AConfirmationRunCutOffIsUnresolved(t *testing.T) {
	root := moduleWithAReachingPackage(t, "\t_ = Size(1)\n")
	t.Cleanup(setResolveBudgetForTest(50 * time.Millisecond))
	fakeResolveExec(t, func(ctx context.Context) (int, error) {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(10 * time.Second):
			return 0, nil
		}
	})

	got := resolveOneForTest(t, root, inconclusiveKind())

	if got.Status != gremlinsScopeUnknown || !strings.HasPrefix(got.Note, "UNRESOLVED:") {
		t.Fatalf("got %q (%s), want it left %q and UNRESOLVED", got.Status, got.Note, gremlinsScopeUnknown)
	}
	if !strings.Contains(got.Note, "did not finish") {
		t.Fatalf("note = %q, want it to say the run without the mutant did not finish, not that it failed", got.Note)
	}
}

// TestResolveGapMutants_AConfirmedKillNamesItsPackage: the failed package
// passes without the mutant, so the failure was the mutant's.
func TestResolveGapMutants_AConfirmedKillNamesItsPackage(t *testing.T) {
	root := moduleWithAReachingPackage(t, "\t_ = Size(1)\n")
	fakeResolveExec(t, func(context.Context) (int, error) { return 0, nil })

	got := resolveOneForTest(t, root, inconclusiveKind())

	if got.Status != "caught" || !strings.Contains(got.Note, "m/other") {
		t.Fatalf("got %q (%s), want caught by m/other", got.Status, got.Note)
	}
}

// TestResolveGapMutants_AKillNamingNoPackageRerunsTheWholeStage: a failed
// run that printed no package FAIL line (a crash before go test's summary)
// is confirmed by running the stage it failed in, not nothing.
func TestResolveGapMutants_AKillNamingNoPackageRerunsTheWholeStage(t *testing.T) {
	root := moduleWithAReachingPackage(t, "\t_ = Size(1)\n")
	var rerun []string
	prev := resolveExecFn
	resolveExecFn = func(_ context.Context, _ string, _ []string, argv []string, log io.Writer) (int, error) {
		for _, a := range argv {
			if a == "-overlay" {
				_, _ = io.Copy(log, bytes.NewBufferString("signal: killed\n"))
				return 1, nil
			}
		}
		rerun = argv
		return 0, nil
	}
	t.Cleanup(func() { resolveExecFn = prev })

	got := resolveOneForTest(t, root, inconclusiveKind())

	if got.Status != "caught" {
		t.Fatalf("got %q (%s), want caught", got.Status, got.Note)
	}
	// expectation-changed: every mutant go test now carries -vet=off, so the confirmation run does too
	if want := "go test -count=1 -failfast -vet=off ./other"; strings.Join(rerun, " ") != want {
		t.Fatalf("confirmation run = %q, want %q", strings.Join(rerun, " "), want)
	}
}
