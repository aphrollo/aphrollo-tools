package tdd

import (
	"reflect"
	"testing"
)

// TestWithGoCIParity_InsertsRaceCountShuffleAndTimeout is issue #421's core
// claim at the unit level: a plain `go test` Runner gets exactly the four
// flags CI's own invocation carries that DetectRunner's default does not,
// in CI's order, right after "test" and ahead of the caller's own args.
func TestWithGoCIParity_InsertsRaceCountShuffleAndTimeout(t *testing.T) {
	got := withGoCIParity(Runner{Cmd: "go", Args: []string{"test", "./internal/x"}})
	want := Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "-timeout=180s", "./internal/x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withGoCIParity = %+v, want %+v", got, want)
	}
}

// TestWithGoCIParity_NeverDoublesAFlagAlreadyPresent guards the idempotency
// the doc comment claims: calling it twice (or handing it a Runner that
// already names one of the four flags) must not repeat a flag.
func TestWithGoCIParity_NeverDoublesAFlagAlreadyPresent(t *testing.T) {
	once := withGoCIParity(Runner{Cmd: "go", Args: []string{"test", "./..."}})
	twice := withGoCIParity(once)
	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("withGoCIParity applied twice = %+v, want unchanged %+v", twice, once)
	}
}

// TestWithGoCIParity_LeavesNonGoTestRunnersUntouched: cargo, vet, and the
// linter must never see these flags — go vet and golangci-lint do not
// understand them, and cargo has its own command shape entirely.
func TestWithGoCIParity_LeavesNonGoTestRunnersUntouched(t *testing.T) {
	for _, r := range []Runner{
		{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}},
		{Cmd: "go", Args: []string{"vet", "./..."}},
		{Cmd: "golangci-lint", Args: []string{"run", "."}},
	} {
		if got := withGoCIParity(r); !reflect.DeepEqual(got, r) {
			t.Fatalf("withGoCIParity(%+v) = %+v, want unchanged", r, got)
		}
	}
}

// TestMechanicalGoSuite_CarriesCIParityFlags proves the premergecommit path
// gets the same treatment as precommit's: issue #421 named BOTH "the
// pre-commit and pre-merge mechanical stages" as the affordable place to
// pay for CI parity, and Mechanical is the pre-merge-commit gate's entry
// point, sharing gateRoot with Precommit.
func TestMechanicalGoSuite_CarriesCIParityFlags(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "-timeout=180s", "./internal/x"}}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("mechanical (premerge) go runner = %+v, want one %+v", seen, want)
	}
}
