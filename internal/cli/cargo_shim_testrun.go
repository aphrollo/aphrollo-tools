package cli

import (
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// cargoTestTargetFlags select test targets explicitly. Any one of them takes
// doctests out of a `cargo test` run; without one, cargo runs the library's
// doctests too, and rustdoc compiles those at RUN time -- a compile the slot
// would no longer cover.
var cargoTestTargetFlags = []string{
	"--lib", "--bin", "--bins", "--test", "--tests",
	"--example", "--examples", "--bench", "--benches", "--all-targets",
}

// cargoTestRunBuildArgs returns the compile-only form of a test or bench
// run, and whether this invocation splits at all. A test or bench binary's
// runtime writes nothing into the target dir, so a slot held across it only blocks other
// builds (issue #727: a ten-minute ignored measurement run queued a merge
// gate in another lane for its whole length). The compile form is the
// cargo-side argv -- everything before the first bare "--", the harness's
// own arguments being meaningless to a build -- plus --no-run: the same
// packages, profile, features and targets, so the run that follows finds
// every unit fresh and executes exactly the binaries built under the slot.
//
// It does not split when the compile form would not cover the whole run:
// `cargo test` with doctests in scope (no target flag, or --doc, which cargo
// refuses to combine with --no-run), or an invocation that already is
// compile-only.
func cargoTestRunBuildArgs(args []string) ([]string, bool) {
	cargoSide := args
	for i, a := range args {
		if a == "--" {
			cargoSide = args[:i]
			break
		}
	}
	if hasCargoFlag(cargoSide, "--no-run") {
		return nil, false
	}
	switch cargoVerb(args) {
	case "test":
		if hasCargoFlag(cargoSide, "--doc") || !hasAnyCargoFlag(cargoSide, cargoTestTargetFlags) {
			return nil, false
		}
	case "bench":
		// `cargo bench` runs no doctests, so its bench binaries are all the
		// run executes.
	case "nextest":
		// nextest never runs doctests. Only its `run` builds and then
		// executes; `list` and `archive` are compiles through and through.
		if nextestSubcommand(cargoSide) != "run" {
			return nil, false
		}
	default:
		return nil, false
	}
	out := make([]string, 0, len(cargoSide)+1)
	out = append(out, cargoSide...)
	return append(out, "--no-run"), true
}

// nextestSubcommand is the first non-option token after the "nextest" verb.
func nextestSubcommand(args []string) string {
	seenVerb := false
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			continue
		}
		if seenVerb {
			return a
		}
		seenVerb = a == "nextest"
	}
	return ""
}

// hasCargoFlag matches a flag in both its bare and its --flag=value form.
func hasCargoFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

func hasAnyCargoFlag(args, flags []string) bool {
	for _, f := range flags {
		if hasCargoFlag(args, f) {
			return true
		}
	}
	return false
}

// runBuildThenRunSlotFree compiles under the slot, releases it, and only
// then runs the original argv. A failing compile returns its own exit code
// and nothing runs. The run phase is told no lock is held and carries no
// slot token: it outlives the slot, so a cargo it spawns that does have to
// compile queues like any other.
func runBuildThenRunSlotFree(slot tdd.BuildSlot, release func(), realCargo string, buildArgs, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	buildCode := execCargo(realCargo, buildArgs, stdin, stdout, stderr, slot.Jobs)
	release()
	if buildCode != 0 {
		return buildCode
	}
	return execCargo(realCargo, args, stdin, stdout, stderr, 0)
}
