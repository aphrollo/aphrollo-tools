package tdd

import (
	"bytes"
	"testing"
)

// stubMutantsProducerVersion pins the mutation tool's own version this
// process reports, so a test can simulate an upgrade between two runs
// without a real toolchain.
func stubMutantsProducerVersion(t *testing.T, version string) {
	t.Helper()
	prev := mutantsProducerVersionRunFn
	mutantsProducerVersionRunFn = func(name string, args []string) (string, error) { return version, nil }
	t.Cleanup(func() { mutantsProducerVersionRunFn = prev })
}

// A file's blob and its package's fence describe the SOURCE, never the tool:
// a gremlins upgrade puts a byte-identical file back into the re-measure set
// (PlanDiffFiles), but before carriesOver learned to compare the producer's
// version too, PlanMutants kept handing that file's OLD cached mutants to the
// carry set unchanged — the same mutant reaching the signed receipt twice,
// once freshly measured under the new version and once as the stale carried
// copy, with `mutants_total` inflated and one mutant able to read as both
// caught and survived at once (issue #298 follow-up).
func TestRunGoMutantsCI_DoesNotCountAMutantTwiceWhenTheProducerVersionChanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := ciRepoWith(t, map[string]string{
		"calc.go": "package m\n\nfunc Calc() int { return 1 }\n",
	})
	store := t.TempDir()

	stubMutantsProducerVersion(t, "gremlins 0.6.0")
	fakeGremlins(t, ciReport, 0)
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base, Store: store}, &bytes.Buffer{})

	// A producer upgrade, the tree otherwise unchanged: calc.go's blob and
	// its module's fence are exactly what the first push measured.
	stubMutantsProducerVersion(t, "gremlins 0.7.0")
	ran2 := fakeGremlins(t, ciReport, 0)
	var out bytes.Buffer
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base, Store: store}, &out)

	if len(*ran2) != 1 {
		t.Fatalf("a producer upgrade over an otherwise unchanged tree must re-measure, got %d call(s)", len(*ran2))
	}

	r := readReceipt(t, gitValue(t, root, "rev-parse", "HEAD:"))
	if r.MutantsTotal != 2 {
		t.Fatalf("MutantsTotal = %d, want 2 (one caught, one survivor) — not double-counted after a producer-version bump: %+v",
			r.MutantsTotal, r.Outcomes)
	}
	seen := map[mutantKey]int{}
	for _, m := range r.Outcomes {
		seen[m.key()]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Fatalf("mutant %+v appears %d time(s) in the receipt, want at most 1: %+v", k, n, r.Outcomes)
		}
	}
}
