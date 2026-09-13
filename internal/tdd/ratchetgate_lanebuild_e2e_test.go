package tdd

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// thisModuleRoot locates the checkout under test by asking the toolchain
// which go.mod is in effect, never by counting `..` segments from the test's
// own directory: a relative walk is exactly what breaks when the tree is
// copied for a mutation run (#262, #112), and `go env` answers correctly
// from a copy, from a worktree and from the module root alike.
func thisModuleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err) // stderr-ok: the toolchain is running this test; its absence is not a case
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatalf("go env GOMOD names no module: %q", gomod)
	}
	return filepath.Dir(gomod)
}

// The stage's unit tests replace laneFixtureBuild and laneFixtureRun, which
// leaves the one thing those two exist for — a real build of this checkout,
// asked a real question over a real process boundary — proved by nothing. If
// the argv this side constructs and the flags `ratchet test` accepts ever
// drift apart, every commit that stages a law is refused with a message
// about the build rather than about the law, and no unit test notices.
func TestLaneFixtureRun_AnswersFromARealBuildOfThisCheckoutAboutOneNamedLaw(t *testing.T) {
	root := thisModuleRoot(t)
	if !IsLawEngineCheckout(root) {
		t.Fatalf("%s is where this test's own source lives; it must be the law engine's checkout", root)
	}

	bin, cleanup, err := laneFixtureBuild(root)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("building this checkout: %v", err)
	}

	results, err := laneFixtureRun(bin, root, []string{"module_size"})
	if err != nil {
		t.Fatalf("asking this checkout's own build for module_size: %v", err)
	}
	if len(results) != 1 || results[0].Law != "module_size" {
		t.Fatalf("--only module_size must answer for module_size and nothing else; got %+v", results)
	}
	if len(results[0].Failures) != 0 {
		t.Fatalf("this repo's own module_size fixtures are sound; failures = %v", results[0].Failures)
	}
	if results[0].HitFiles == 0 || results[0].CleanFiles == 0 {
		t.Fatalf("both directions must be counted across the process boundary; got %+v", results[0])
	}
}
