package tdd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// What the run found, turned into the one report a merge reads. The order is
// the whole design: the surviving MUTANT first, because that is the finding,
// then the counts, then the remedy. The stage this replaces put seven
// paperwork checks ahead of the question "did a mutant survive", and over
// three weeks it refused 150 merges without naming a survivor once.

// finishMeasure judges the run's outcomes, records the verdict in the gate
// log and gives the repo's own after-hook the result. It is the one place a
// measurement becomes a verdict, so a Cargo run and a Go run cannot disagree
// about what a survivor means.
func finishMeasure(root string, cfg MutantsConfig, mutants []MutantOutcome, log io.Writer) Verdict {
	v := judgeMutants(cfg, mutants)
	logf(log, "%s", v.Message)
	appendGateLog("mutants", measureLogRoot(root), "mutants", measureLogVerdict(v), 0)
	runMutantsAfter(root, cfg, v, log)
	return v
}

// judgeMutants splits the run's outcomes by the accept-list and renders
// criterion 12's report. It touches no disk and no clock, so what a verdict
// says is a function of what was measured and what the repo declared.
func judgeMutants(cfg MutantsConfig, mutants []MutantOutcome) Verdict {
	list, bad := parseAcceptedMutants(cfg.Accept)
	if len(bad) > 0 {
		return refusedAcceptList(bad)
	}
	var v Verdict
	var missed, timedOut []MutantOutcome
	for _, m := range mutants {
		v.Tested++
		switch m.Status {
		case "caught":
			v.Caught++
		case "missed":
			missed = append(missed, m)
		case "timeout":
			timedOut = append(timedOut, m)
		case gremlinsNotCovered:
			// Counted apart from unviable: "no coverage block maps here" is
			// a different claim from "this mutant does not compile", and it
			// is the one that goes to 100% of a module when the coverage
			// mapping breaks. Neither refuses a merge on its own.
			v.NotCovered++
		default:
			v.Unviable++
		}
	}
	accepted, unaccepted, _, notes := splitAcceptedSurvivors(list, missed)
	v.Missed, v.Accepted = len(missed), len(accepted)
	v.Unaccepted, v.Unmeasured = unaccepted, timedOut
	v.Refused = len(unaccepted) > 0 || len(timedOut) > 0
	v.Message = measureReport(v, notes)
	if v.Tested == 0 && !v.Refused {
		// Not a refusal: a diff the tool produces no mutants for is a real
		// and legitimate outcome (a change with nothing mutatable in it).
		// But it is not a MEASUREMENT either, and the one thing it must not
		// be is indistinguishable from a run that caught everything — which
		// is what "mutants-passed:tested=0" was, in the report and in the
		// column `gate stats` puts it in.
		v.Skipped = zeroTestedNote
		v.Message = "mutants: " + zeroTestedNote + "\n" + v.Message
	}
	return v
}

// zeroTestedNote is how a run with an empty mutant pool names itself, and
// zeroTestedToken is what `gate stats` counts it under — apart from the green
// column, which is reserved for runs that actually proved something.
const (
	zeroTestedNote  = "no mutants were tested — the tool produced none for this diff, so nothing here was measured (not a pass)"
	zeroTestedToken = "no-mutants"
)

// refusedAcceptList is the verdict for an accept-list that could not be read.
// Every bad entry is quoted: a misspelled kind must never look like it landed
// as an ordinary equivalence claim, and a list nobody had to justify is a
// list of survivors somebody silenced.
func refusedAcceptList(bad []string) Verdict {
	var b strings.Builder
	for _, entry := range bad {
		fmt.Fprintf(&b, "mutation-accept entry refused: %q\n", entry)
	}
	b.WriteString("mutants: the accept-list could not be read, so no survivor could be judged against it\n")
	b.WriteString(measureRemedy)
	return Verdict{Refused: true, Message: b.String()}
}

// measureRemedy is the line that says what to do about a survivor. Both
// answers are legitimate and the entry has to carry its reason either way.
const measureRemedy = "mutants: write the test that fails, or add the line to mutation-accept with a reason " +
	"(\"<file>:<line>:<col> <mutation> # kind=<equivalent|unobservable-runner|unobservable-capability>: why\")"

// measureReport renders the verdict: the mutants first, then one summary
// line, then the remedy when there is something to remedy.
func measureReport(v Verdict, notes []acceptNote) string {
	var b strings.Builder
	for _, m := range v.Unaccepted {
		b.WriteString(outcomeName(m) + "\n")
	}
	for _, m := range v.Unmeasured {
		b.WriteString(outcomeName(m) + " — timed out twice, unmeasured\n")
	}
	for _, n := range notes {
		if n.Refused {
			fmt.Fprintf(&b, "mutation-accept entry refused (%s)\n", n.Text)
			continue
		}
		// Applied, not refused: the report says what the entry admitted, so a
		// reviewer sees it, and the verdict itself is unchanged.
		fmt.Fprintf(&b, "mutation-accept: %s\n", n.Text)
	}
	fmt.Fprintf(&b, "mutants: %d tested, %d caught, %d unviable, %d missed (%d accepted), %d unmeasured",
		v.Tested, v.Caught, v.Unviable, v.Missed, v.Accepted, len(v.Unmeasured))
	if v.NotCovered > 0 {
		// Only when there are any: a Cargo run has no such category at all,
		// and a trailing ", 0 not covered" on every one of its reports is a
		// column about a tool it does not use.
		fmt.Fprintf(&b, ", %d not covered", v.NotCovered)
	}
	if v.Refused {
		b.WriteString("\n" + measureRemedy)
	}
	return b.String()
}

// measureSkipped is the verdict for a run that never started, with the reason
// a human reads and the token `gate stats` counts.
func measureSkipped(root, reason, token string, log io.Writer) Verdict {
	v := Verdict{Skipped: reason, Message: "mutants: " + reason}
	logf(log, "%s", v.Message)
	appendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-skipped:"+token, 0)
	return v
}

// measureNoVerdict is the refusal for a run that stopped without reaching
// one: the exit status, whatever went wrong reading its outcomes, and the log
// that explains it. Never "0 missed" from a file that was never written.
// logDir is the failing SHARD's own log directory — with N processes writing
// N logs, a refusal pointing at the run's general area names none of them.
func measureNoVerdict(root, logDir string, code int, cause error, log io.Writer) Verdict {
	msg := fmt.Sprintf("mutants: the run exited %d and reached no verdict — see %s", code, logDir)
	if cause != nil {
		msg += fmt.Sprintf(" (%v)", cause)
	}
	// What the drive looks like right now, when that is itself the answer: a
	// run killed by a full disk otherwise reports its exit status and its log
	// directory and nothing about the reason (issue #600).
	msg += measureDiskNote(root)
	logf(log, "%s", msg)
	appendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-refused:no-verdict", 0)
	return Verdict{Refused: true, Message: msg}
}

// measureLogRoot is how the gate log names the tree that was measured.
func measureLogRoot(root string) string {
	if r := RepoRoot(root); r != "" {
		return logToken(r)
	}
	return logToken(root)
}

// measureLogVerdict carries criterion 12's counts into the gate log, so
// `gate stats` can answer how many merges the stage refused and for what
// without re-running anything.
func measureLogVerdict(v Verdict) string {
	if v.Tested == 0 && !v.Refused {
		// A run with nothing in the pool is filed as the stand-down it is:
		// the counted form would add a green to the table that says how often
		// this lane's tests were actually held against a mutant.
		return "mutants-skipped:" + zeroTestedToken
	}
	state := "passed"
	if v.Refused {
		state = "refused"
	}
	return fmt.Sprintf("mutants-%s:tested=%d,caught=%d,unviable=%d,missed=%d,accepted=%d,unmeasured=%d,notcovered=%d",
		state, v.Tested, v.Caught, v.Unviable, v.Missed, v.Accepted, len(v.Unmeasured), v.NotCovered)
}

// mutantsAfterStatusEnv carries the measurement's own verdict to the repo's
// post-run command: "0" for a run that passed, "1" for one that was refused.
const mutantsAfterStatusEnv = "APHROLLO_MUTANTS_STATUS"

// runMutantsAfter runs the repo's own post-run command in the worktree, with
// the verdict in APHROLLO_MUTANTS_STATUS. The binary must not know what a
// test's side effects are: a repo whose tier writes to a shared database owns
// reclaiming the rows a timeout-killed test binary left behind, and this is
// the one hook it gets. Its own failure is logged and never changes the
// verdict — a cleanup script that fails must not turn a clean measurement
// into a refused merge, nor a refused one into a pass.
func runMutantsAfter(root string, cfg MutantsConfig, v Verdict, log io.Writer) {
	if cfg.After == "" {
		return
	}
	status := "0"
	if v.Refused {
		status = "1"
	}
	path := filepath.Join(root, filepath.FromSlash(cfg.After))
	argv := []string{path}
	if strings.HasSuffix(cfg.After, ".sh") {
		// A checked-in shell script is not executable on every box this runs
		// on, and the repo's own runner was invoked exactly this way.
		argv = []string{"bash", filepath.ToSlash(path)}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), mutantsAfterStatusEnv+"="+status)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	logf(log, "mutants: mutants-after exited %d — %v; the verdict is unchanged", code, err)
	if text := strings.TrimSpace(string(out)); text != "" {
		logf(log, "mutants: mutants-after said: %s", text)
	}
}

// A mutant that times out is normally a busy box rather than a hang, so the
// budget has to be derived from what the unmutated suite ACTUALLY took on
// this box rather than from a constant. Each run records its own baseline for
// the next one; a repo with no record yet gets the floor.

// mutantsMinTestTimeoutFloor is the smallest budget a mutant's suite gets,
// whatever the last baseline said. Nine timeouts on one lane were measured at
// cargo-mutants' 30 s default while eight cold builds shared the box.
const mutantsMinTestTimeoutFloor = 120

// mutantsMinTestTimeout is `max(3 × last baseline seconds, 120)`.
func mutantsMinTestTimeout(root string) int {
	seconds := readMutantsBaselineSeconds(root)
	if budget := int(seconds * mutantsTimeoutMultiplier); budget > mutantsMinTestTimeoutFloor {
		return budget
	}
	return mutantsMinTestTimeoutFloor
}

// mutantsBaselinePath is where one repo's last measured baseline lives:
// beside the gate's other state, keyed on the repo, never in the tree.
func mutantsBaselinePath(root string) string {
	dir := mutantsLogDir(root)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "baseline_seconds")
}

// unmutatedBaselineRe reads cargo-mutants' own report of what the unmutated
// suite cost: "ok       Unmutated baseline in 12.5s build + 30.0s test".
var unmutatedBaselineRe = regexp.MustCompile(`Unmutated baseline in ([0-9.]+)s build \+ ([0-9.]+)s test`)

// recordMutantsBaseline keeps what THIS run measured, for the next run's
// budget. A log with no baseline line (a run that skipped it, or one that
// died before it) leaves the previous record alone.
func recordMutantsBaseline(root, runLog string) {
	m := unmutatedBaselineRe.FindStringSubmatch(runLog)
	if m == nil {
		return
	}
	build, buildErr := strconv.ParseFloat(m[1], 64)
	test, testErr := strconv.ParseFloat(m[2], 64)
	if buildErr != nil || testErr != nil {
		return
	}
	writeMutantsBaselineSeconds(root, build+test)
}

// writeMutantsBaselineSeconds records one baseline measurement.
func writeMutantsBaselineSeconds(root string, seconds float64) {
	path := mutantsBaselinePath(root)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(strconv.FormatFloat(seconds, 'f', -1, 64)), 0o600)
}

// readMutantsBaselineSeconds is the last recorded baseline, 0 when there is
// none — which the floor then decides.
func readMutantsBaselineSeconds(root string) float64 {
	seconds, err := strconv.ParseFloat(readMutantsBaselineSecondsText(root), 64)
	if err != nil {
		return 0
	}
	return seconds
}

// readMutantsBaselineSecondsText is the recorded baseline as written.
func readMutantsBaselineSecondsText(root string) string {
	path := mutantsBaselinePath(root)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
