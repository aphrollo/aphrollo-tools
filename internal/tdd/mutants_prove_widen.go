package tdd

import (
	"fmt"
	"strings"
)

// Issue #691, both languages, one rule. A proof's related-tests selection is
// narrowed: a Rust `src/` mutation to `--lib` plus the file's module filter,
// a Go one to `go test ./<dir>`. Neither narrowing can reach the test that
// kills a mutant constrained only from OUTSIDE it — an integration binary
// under tests/, a test in a package that imports the mutated one — so that
// selection came back green and the proof called it a SURVIVOR. A confident
// wrong answer in the direction that BLOCKS correct work, since the pre-merge
// gate refuses an unaccepted survivor by name, and a silent one: nothing in
// the output said the run could not have reached the killing test.
//
// The rule is the one NoTestsSelected enforces one branch over: a verdict may
// only claim what the run it was read from could have observed. "Nothing in
// this selection killed it" is not "nothing kills it".
//
// Two phases rather than always running the wider selection, because the
// branches are neither equally likely nor equally expensive. A KILL settles
// on the cheap narrow run and stops there; only a green — the rare branch —
// pays for the integration binaries or the importers' suites, and it pays
// exactly where the one-phase answer would have been wrong. The trap of a
// two-phase verdict is recording the wrong phase, so the widened runner and
// its result REPLACE the narrow pair for everything downstream: the verdict
// printed, the tests read out of it, and the run retained for `aphrollo gate
// output`.
//
// The two arms agree on what a verdict MEANS, deliberately — someone reading
// a SURVIVOR should not have to know which language produced it:
//
//   - both widen only on a green, never on a red or a timeout;
//   - the recorded verdict, the names read out of it and the retained run all
//     come from the widened run;
//   - a selection that was ALREADY as wide as it goes (no narrowing left to
//     drop; a package nothing imports) keeps its SURVIVOR, because it did
//     cover every test that could have killed the mutant;
//   - a selection that CANNOT be widened, or whose reach cannot be read at
//     all, is INCONCLUSIVE (ExitMutantsProveScopeUnknown) and never a
//     survivor.
//
// Only the diff-scoped Rust measurement (`aphrollo gate mutants run`) is
// exempt, and by construction rather than by luck: cargo-mutants runs the
// package's whole nextest suite for every mutant, with no target narrowing to
// widen (MutantsArgv, pinned by its own test). The Go measurement is NOT
// exempt — gremlins runs the mutated package's own tests, measured — but that
// is the tool's own selection, not an argv this repo builds.

// widenOutcome is what the widening attempt concluded.
type widenOutcome int

const (
	// widenNotNeeded: the selection that ran could already reach every test
	// that could kill the mutant, so its verdict stands as it is.
	widenNotNeeded widenOutcome = iota
	// widenDone: a wider selection was run, and ITS pair is the one to judge.
	widenDone
	// widenUnknown: what reaches the mutated code could not be established,
	// so whether the selection was the whole story is unknown — the one
	// outcome that must never settle as a survivor.
	widenUnknown
)

// widenedSelection is what the widening concluded: the runner and result the
// caller must go on judging (the widened pair once a wider run was made), the
// outcome, and — for widenUnknown only — the reason the reach could not be
// established, which the inconclusive verdict has to be able to name.
type widenedSelection struct {
	runner  Runner
	res     SuiteResult
	outcome widenOutcome
	why     error
}

// widenSurvivorSelection re-runs a proof's green run over everything that
// could kill the mutant, and hands back the pair the caller must judge on.
//
// It fires only where a SURVIVOR would otherwise be claimed. A red run has
// already observed something and is judged on its own name; a timeout says
// nothing about selection; a run that selected zero tests has its own
// refusal, which is already honest about having tested nothing.
func widenSurvivorSelection(run SuiteRunner, narrow Runner, root string, res SuiteResult) widenedSelection {
	if res.TimedOut || !res.Passed || selectedZeroTests(narrow, res) {
		return widenedSelection{runner: narrow, res: res, outcome: widenNotNeeded}
	}
	wide, outcome, why := widenToEveryTestThatCouldKill(narrow, root)
	if outcome != widenDone {
		return widenedSelection{runner: narrow, res: res, outcome: outcome, why: why}
	}
	return widenedSelection{runner: wide, res: run(wide, root), outcome: widenDone}
}

// widenToEveryTestThatCouldKill builds the selection that covers every test
// able to kill a mutant the narrowed run left unkilled. A runner neither arm
// can reason about reports widenNotNeeded: there is no narrowing of OURS on
// it to undo, so its verdict is the one it already produced.
func widenToEveryTestThatCouldKill(narrow Runner, root string) (Runner, widenOutcome, error) {
	switch narrow.Cmd {
	case "cargo":
		return widenCargoSelection(narrow)
	case "go":
		return widenGoSelection(narrow, root)
	}
	return Runner{}, widenNotNeeded, nil
}

// widenCargoSelection drops the within-package narrowing — the target
// selection and the name filter — and keeps the package scope, via the
// post-edit half's own widenCargoRunner (#642), so "as wide as this crate
// goes" means one thing across the gate.
//
// Nothing left to drop means the run already covered the crate, so its
// SURVIVOR stands.
func widenCargoSelection(narrow Runner) (Runner, widenOutcome, error) {
	wide, ok := widenCargoRunner(narrow)
	if !ok {
		return Runner{}, widenNotNeeded, nil
	}
	return wide, widenDone, nil
}

// widenGoSelection widens to every package whose TEST binary can reach one of
// the narrowed run's packages, the mutated one included. A `go test ./...`
// that is already the whole module, or a runner naming no package at all, has
// nothing to add.
func widenGoSelection(narrow Runner, root string) (Runner, widenOutcome, error) {
	selected, ok := goSelectedDirs(narrow)
	if !ok {
		return Runner{}, widenNotNeeded, nil
	}
	var reaching []string
	for _, dir := range selected {
		more, err := goTestReachFn(root, dir)
		if err != nil {
			// The graph could not be read. NOT an empty answer: "nothing
			// else reaches this package" and "I could not find out" are
			// opposite claims, and only the first one could ever ground a
			// survivor.
			return Runner{}, widenUnknown, err
		}
		reaching = append(reaching, more...)
	}
	reaching = dedupeSorted(reaching)
	if len(reaching) == len(selected) {
		// Same set: dedupeSorted over the reach always contains the selected
		// dirs themselves, so equal lengths mean nothing was added and the
		// narrow run already covered every test that could kill the mutant.
		return Runner{}, widenNotNeeded, nil
	}
	args := make([]string, 0, len(reaching)+1)
	args = append(args, "test")
	for _, dir := range reaching {
		args = append(args, "./"+dir)
	}
	return Runner{Cmd: "go", Args: args, Dir: narrow.Dir}, widenDone, nil
}

// goSelectedDirs reads the package directories a narrowed go runner selects,
// in goPackageDir's dialect ("." for the repo root). ok is false when the
// runner names no package at all, or names the whole module (`./...`) — the
// widening has nothing to add to either.
func goSelectedDirs(r Runner) ([]string, bool) {
	var dirs []string
	for _, a := range r.Args[1:] {
		if strings.HasPrefix(a, "-") || a == "test" {
			continue
		}
		if a == "./..." || a == "all" {
			return nil, false
		}
		dir := strings.TrimSuffix(strings.TrimPrefix(a, "./"), "/...")
		if dir == "" {
			dir = "."
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		return nil, false
	}
	return dedupeSorted(dirs), true
}

// widenedProveNote is what a verdict reached through both phases says about
// the first one. A reader auditing a survivor has to be able to tell which
// selection produced it, and a reader auditing a kill has to be able to tell
// why the proof ran twice.
func widenedProveNote(narrow Runner, outcome widenOutcome) string {
	if outcome != widenDone {
		return ""
	}
	return fmt.Sprintf(" Widened first: %s stayed green, and a narrowed selection cannot run a killing test "+
		"outside what it selected, so this verdict is the wider run's.", cmdString(narrow))
}

// scopeUnknownAdvisory is the inconclusive verdict for a green run that could
// not be widened into one able to ground a survivor claim. It reads like its
// siblings (noTestsSelectedAdvisory, the timeout) because it means the same
// thing — what the tests constrain was NOT established — and it carries the
// arm's own reason, because "inconclusive" with no cause leaves the reader
// nothing to act on.
func scopeUnknownAdvisory(narrow Runner, relPath string, why error) string {
	return fmt.Sprintf("gate: mutant SCOPE UNKNOWN — %s stayed green, but that selection cannot ground a "+
		"survivor claim about %s: %v. INCONCLUSIVE — nothing is proved either way, because a survivor claim "+
		"asserts that no test kills the line and this run could not establish which tests reach it. Restored — "+
		"re-run where the wider selection can be built, or run the tests that reach this code by hand.\n",
		cmdString(narrow), relPath, why)
}
