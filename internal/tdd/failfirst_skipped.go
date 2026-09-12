package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A SKIP is not a pass. Every other stage in this binary already says so —
// TIMEOUT, SKIPPED, QUEUED-SKIPPED, NoTestsSelected and BuildOnly are all the
// inconclusive family, "the code was NOT tested" — and fail-first, judging
// the single most important question in the pipeline (did this test ever go
// RED), was reading a skip as a pass.
//
// Field evidence, issue #656: a commit carrying a GPU device test behind an
// env switch, plus the implementation it pins.
//
//	[fail-first] gate precommit: cargo test -p particles --test gpu_parity → violated (28.7s)
//	TDD fail-first: this commit adds tests AND implementation, but the new
//	tests PASS against the pre-edit code (HEAD) …
//
// The test did not pass against HEAD. With the switch unset it SELF-SKIPPED,
// and the proof run reported exit 0. The identical commit with the switch
// exported reached red-proven in 34.8s. So the gate blocked a correct commit
// and told its author their test never went RED, when in fact it never RAN.
//
// This file is the distinction the stage was missing, and failfirst_env.go is
// the remedy it points at. The two are separate on purpose: declaring the
// switch makes the proof RUN, and this verdict is what happens when a proof
// ran anyway and measured nothing.
//
// Where the case is DISTINCT from emptyselection.go's: there, the run
// selected zero tests — a filter matched nothing. Here the tests were
// selected, the binary executed them, and each one decided at RUNTIME to do
// nothing. Same family, same reporting shape, different cause and different
// remedy, so it gets its own verdict rather than being folded into
// NoTestsSelected.

// AllTestsSkipped is the verdict for a run whose SELECTED tests every one
// skipped themselves. Inconclusive family, alongside NoTestsSelected,
// BuildOnly, TIMEOUT/SKIPPED/QUEUED-SKIPPED, DeferredAbandoned and
// InfraFailed — the code was NOT tested.
const AllTestsSkipped = "all-tests-skipped"

// skippedOnlyNames returns the names (package, target, or the runner itself)
// whose whole selected set skipped, nil when at least one test reached a real
// verdict or when this runner's output cannot answer the question. It is
// vacuousNames' sibling and is called in the same place, on a PASSED,
// non-timed-out run: zero tests EXECUTED is vacuousNames', executed-and-all-
// skipped is this one's, and the two shapes are disjoint by construction in
// every runner below.
//
// Only Go's check can itself fail, for the same reason vacuousNames' can: it
// decodes a `go test -json` stream that a killed process can truncate, and a
// stream this cannot finish reading is reported as unreadable rather than
// judged on its prefix.
func skippedOnlyNames(runner Runner, res SuiteResult) ([]string, error) {
	switch runner.Cmd {
	case "go":
		return goSkippedOnlyPackages(res.GoTestJSON)
	case "cargo":
		return cargoSkippedOnlyTargets(runner, res.Output), nil
	case "pytest":
		if pytestSkippedOnly(res.Output) {
			return []string{"pytest"}, nil
		}
		return nil, nil
	default:
		// vitest, zig, npm scripts, anything else: this gate reads no skip
		// signal out of their output, so it claims none. Saying "not
		// skipped" for a runner we cannot read would be the same guess in
		// the other direction, which is why failFirstViolationMessage names
		// the limit instead of this function papering over it.
		return nil, nil
	}
}

// skipReadableRunners are the runners whose SKIP this gate can actually read
// out of a run's output. Anything else is judged on the pass/fail bit alone,
// and the violation message says so by name.
var skipReadableRunners = map[string]bool{"go": true, "cargo": true, "pytest": true}

// goSkippedOnlyPackages returns the packages whose `go test -json` stream
// shows at least one per-test SKIP and not one per-test pass or fail. go
// test's own package verdict for such a package is "ok" (Action "pass"), so
// from the outside it is indistinguishable from a real green — the per-test
// events are the only place the fact is stated.
//
// A package with no per-test event at all is vacuousGoPackages' case, not
// this one, and is left to it: this function requires a skip to have been
// SEEN before it will name a package.
func goSkippedOnlyPackages(rawJSON string) ([]string, error) {
	skipped := map[string]bool{}
	decided := map[string]bool{}
	dec := json.NewDecoder(strings.NewReader(rawJSON))
	for {
		var e goTestEvent
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("goSkippedOnlyPackages: reading go test -json stream: %w", err)
		}
		if e.Package == "" || e.Test == "" {
			continue
		}
		switch e.Action {
		case "skip":
			skipped[e.Package] = true
		case "pass", "fail":
			decided[e.Package] = true
		}
	}
	var out []string
	for pkg := range skipped {
		if !decided[pkg] {
			out = append(out, pkg)
		}
	}
	sort.Strings(out)
	return out, nil
}

// nextestSkippedRe reads cargo-nextest's own summary line, which counts the
// skipped tests separately from the ones it ran:
//
//	Summary [   0.011s] 0 tests run: 0 passed, 1 skipped
//
// Zero run WITH a nonzero skipped count is the whole selection skipping
// itself. Zero run with nothing skipped is emptyselection.go's
// NoTestsSelected, and nextestSummaryRe already owns that shape.
var nextestSkippedRe = regexp.MustCompile(`(?m)^\s*Summary\s*\[[^\]]*\]\s*(\d+)\s*tests?\s*run:.*?(\d+)\s*skipped`)

// cargoSkippedOnlyTargets names the libtest targets whose every selected test
// was skipped, or the nextest run as a whole when its summary says so.
//
// libtest's shape is `ignored`: a target reporting zero passed, zero failed
// and a NONZERO ignored count ran nothing that asserted anything. That is a
// deliberate re-reading of the same counts vacuous_cargo.go documents — for
// "did this target execute any test at all" an #[ignore]d test counts as
// executed, because libtest itself decided it; for "did this run prove
// anything about HEAD" it does not, because no assertion in it ever ran.
// Both readings are correct for their own question, and this comment is the
// only place that split is recorded.
//
// What libtest CANNOT report, in any dialect: a test whose body returns early
// on its own (`if env::var("FORGE_GPU_TESTS").is_err() { return; }`), which is
// the commonest way a Rust device suite gates itself. libtest counts it as
// passed and there is no signal to read. failFirstViolationMessage names that
// limit where an author will see it rather than this gate pretending to a
// certainty it does not have.
func cargoSkippedOnlyTargets(runner Runner, output string) []string {
	if m := nextestSkippedRe.FindStringSubmatch(output); m != nil {
		if m[1] == "0" && m[2] != "0" {
			return []string{cmdString(runner)}
		}
		return nil
	}
	blocks := cargoTestResultRe.FindAllStringSubmatchIndex(output, -1)
	if len(blocks) == 0 {
		return nil
	}
	nameAt := cargoTargetNamer(output)
	var out []string
	seen := map[string]bool{}
	for _, m := range blocks {
		passed, _ := strconv.Atoi(output[m[2]:m[3]])
		failed, _ := strconv.Atoi(output[m[4]:m[5]])
		ignored, _ := strconv.Atoi(output[m[6]:m[7]])
		if passed != 0 || failed != 0 || ignored == 0 {
			continue
		}
		if name := nameAt(m[0]); !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// pytestSkippedOnly reports whether pytest's summary shows skipped tests and
// no other outcome at all: `1 skipped in 0.01s`. xfailed and xpassed are
// outcomes pytest reached ABOUT a test that ran, so either one keeps the run
// out of this verdict; deselected is a selection fact, pytestVacuous' case,
// and neither proves nor disproves anything here.
func pytestSkippedOnly(output string) bool {
	skipped, decided := 0, 0
	for _, m := range pytestSummaryCategoryRe.FindAllStringSubmatch(output, -1) {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "skipped":
			skipped += n
		case "deselected":
		default:
			decided += n
		}
	}
	return skipped > 0 && decided == 0
}

// allTestsSkippedMessage is the block for a proof whose tests all skipped. It
// says what happened, refuses to call it either a pass or a red, and hands
// over the one-line remedy — the same shape the #561 refusals use.
func allTestsSkippedMessage(names []string, runner Runner) string {
	return fmt.Sprintf(
		"BLOCKED: fail-first could not prove your staged tests RED — every test the proof selected SKIPPED itself at HEAD (%s), so nothing ran that could go red or green there.\n"+
			"A skip is not a pass. Refusing rather than accusing a correct commit of shipping a test that never failed.\n"+
			"Remedy: if the suite is gated behind an environment switch, declare it once and every fail-first proof inherits it:\n"+
			"    [workspace.metadata.aphrollo]   # or [aphrollo] in aphrollo.toml\n"+
			"    %s = [\"YOUR_SWITCH=1\"]\n"+
			"Otherwise remove the skip from the staged test, or split it into a test that runs unconditionally.\n"+
			"The proof it ran: %s",
		strings.Join(names, ", "), failFirstEnvKey, cmdString(runner))
}

// failFirstViolationMessage is the real violation — the staged tests ran at
// HEAD and PASSED — with the caveat #656 was made of appended. The caveat is
// here and not in a doc because this text is the only thing an author reads
// when the gate refuses them: a test that skips itself by returning early is
// counted as passed by every runner this gate supports, and for a runner
// whose skips it cannot read at all the whole verdict rests on an exit code.
func failFirstViolationMessage(runner Runner) string {
	caveat := fmt.Sprintf(
		"\nIf these tests are gated behind an environment switch they may have SELF-SKIPPED rather than passed: a test that returns early on its own is counted as passed by every runner this gate reads. "+
			"Declare the switch once as %s = [\"YOUR_SWITCH=1\"] under [workspace.metadata.aphrollo] (or [aphrollo] in aphrollo.toml) and the proof will run with it.",
		failFirstEnvKey)
	if !skipReadableRunners[runner.Cmd] {
		caveat += fmt.Sprintf(" For %s this gate cannot read a skip out of the run's output at all, so an explicit skip is invisible to it too.", runner.Cmd)
	}
	return failFirstMessage + caveat
}
