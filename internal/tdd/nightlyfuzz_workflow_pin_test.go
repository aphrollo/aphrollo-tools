package tdd

import (
	"strings"
	"testing"
)

// noFuzzTestsWarning is the exact text `go test -fuzz` prints on STDERR (with
// an EXIT CODE OF ZERO) when the `-fuzz` regex matches no fuzz test in the
// package -- the shape a re-keyed nightly-fuzz.yml target map hits silently:
// the step's own `if go test ...; then echo "no crash"; continue; fi` reads
// that zero exit as "ran clean" with nobody ever having fuzzed anything.
const noFuzzTestsWarning = "testing: warning: no fuzz tests to fuzz"

// TestNightlyFuzzWorkflow_TargetsOnePackageNotItsSubtree pins the `go test`
// invocation inside nightly-fuzz.yml's fuzz step to a single package
// (`"./${pkg}"`), never that package's subtree (`"./${pkg}/..."`): Go's
// `-fuzz` flag refuses to run across more than one matched package, so the
// `/...` form the internal/tdd split's carve-out PRs will re-key this map
// against a subpackage would break the very targets it re-keys.
func TestNightlyFuzzWorkflow_TargetsOnePackageNotItsSubtree(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-fuzz.yml")

	const want = `go test "./${pkg}" -run=NONE -fuzz="^${name}\$" -fuzztime=60s`
	if !strings.Contains(wf, want) {
		t.Fatalf("nightly-fuzz.yml does not invoke go test as %q -- the fuzz step must target exactly one package, since -fuzz refuses to run across several", want)
	}

	const rejected = `"./${pkg}/..."`
	if strings.Contains(wf, rejected) {
		t.Fatalf("nightly-fuzz.yml still passes %q to go test -fuzz -- that matches more than one package and -fuzz refuses to run across several", rejected)
	}
}

// TestNightlyFuzzWorkflow_FailsOnNoFuzzTestsWarning pins the fuzz step to
// treating go test's own "no fuzz tests to fuzz" warning as a job failure
// (setting overall=1), rather than the zero exit code go test itself reports
// for that case. Without this, a target re-keyed to a package or name that no
// longer has a matching fuzz test would report "no crash" every night forever
// and nobody would notice the target had gone silently unfuzzed.
func TestNightlyFuzzWorkflow_FailsOnNoFuzzTestsWarning(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-fuzz.yml")

	idx := strings.Index(wf, noFuzzTestsWarning)
	if idx < 0 {
		t.Fatalf("nightly-fuzz.yml's fuzz step never checks the log for %q -- a re-keyed target matching nothing would report a clean exit and pass silently", noFuzzTestsWarning)
	}

	// The warning text lives inside a shell string (grep pattern or comment);
	// the failure marker must appear within a few lines of it, mirroring the
	// standdown_logged law's own "marker-within-lines" shape, so a check that
	// merely MENTIONS the phrase without acting on it does not pass.
	lines := strings.Split(wf, "\n")
	warnLine := strings.Count(wf[:idx], "\n")
	const window = 5
	lo := warnLine - window
	if lo < 0 {
		lo = 0
	}
	hi := warnLine + window
	if hi > len(lines) {
		hi = len(lines)
	}
	nearby := strings.Join(lines[lo:hi], "\n")
	if !strings.Contains(nearby, "overall=1") {
		t.Fatalf("nightly-fuzz.yml checks for %q but does not set overall=1 near it -- the job must FAIL when a target matches no fuzz test, not merely log it", noFuzzTestsWarning)
	}
}
