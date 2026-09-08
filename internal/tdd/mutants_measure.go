package tdd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// The lane is measured HERE, in the foreground, on the tree that is about to
// land — not in a detached background run whose result a document later
// asserts. Measured over three weeks of gate log, that document refused 150
// merges, logged no reason for 141 of them and named a surviving mutant in
// none; meanwhile the background run held the build lock long enough to queue
// an editor's post-edit hook 619 s behind it.
//
// So: one call, one tree, one verdict. MeasureLane runs the tool, reads its
// machine-readable outcomes, judges them against the repo's own accept-list
// and reports what it found. There is no store, no carry and no receipt: with
// the measurement and the judgement collapsed into one event there is nothing
// left for a cache to carry between them.

// MeasureOpts is what the caller knows that the repo's config does not.
type MeasureOpts struct {
	Base string    // sha or ref the lane is measured against
	Jobs int       // 0 = derive from the box
	Log  io.Writer // the run's own narrative; never the verdict
}

// Verdict is one measurement's whole answer. A verdict is never an error: a
// refused merge is a fact the caller prints, and an error is reserved for the
// runner failing to start at all.
type Verdict struct {
	Refused  bool
	Skipped  string // non-empty when nothing ran, with the reason
	Tested   int
	Caught   int
	Unviable int
	Missed   int
	Accepted int
	// NotCovered is gremlins' NOT COVERED, counted apart from Unviable. It
	// is not "a test ran and did not notice" but "no coverage block maps
	// here", which on Windows it reports for every mutant in a module —
	// folded into unviable that fact disappears, and a report that is mostly
	// uncovered reads like a report that is mostly fine.
	NotCovered int
	Unaccepted []MutantOutcome // missed and not in mutation-accept
	Unmeasured []MutantOutcome // timed out twice
	Message    string          // criterion 12's report, verbatim
}

// mutantsExecFn runs one mutation tool and reports its exit code. A seam, the
// same shape as mutantsProducerFn: a test proves what the runner decides
// without a toolchain on the box.
var mutantsExecFn = runMutantsTool

// runMutantsTool is the real spawn. argv[0] is the program — `cargo` for a
// Cargo lane, `gremlins` for a Go one — so one seam covers both runners.
func runMutantsTool(dir string, env []string, argv []string, log io.Writer) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// SetMutantsExecForTest replaces that seam for one test and answers the
// restore. Exported because what the CLI layer does with a verdict — the
// report it prints, the stream it prints it on, the exit code it turns it
// into — is only provable from the package that owns the command, and no box
// running that test has a mutation tool installed.
func SetMutantsExecForTest(fn func(dir string, env, argv []string, log io.Writer) (int, error)) (restore func()) {
	prev := mutantsExecFn
	mutantsExecFn = fn
	return func() { mutantsExecFn = prev }
}

// mutantsGOOSFn names the platform the stage judges itself on, a seam so the
// Windows stand-down can be proved on any box.
var mutantsGOOSFn = func() string { return runtime.GOOS }

// setMutantsGOOSForTest forces the platform for the duration of a test.
func setMutantsGOOSForTest(goos string) (restore func()) {
	prev := mutantsGOOSFn
	mutantsGOOSFn = func() string { return goos }
	return func() { mutantsGOOSFn = prev }
}

// MeasureLane measures root's changes against opts.Base in root itself
// (cargo-mutants --in-place, or gremlins), judges them against the repo's
// mutation-accept, runs mutants-after, and returns the verdict. A verdict is
// never an error; an error is the runner failing to start.
func MeasureLane(root string, cfg MutantsConfig, opts MeasureOpts) (Verdict, error) {
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	base := strings.TrimSpace(opts.Base)
	if base == "" {
		// `gate mutants run` in a lane with no --base: the newest trunk
		// commit the lane already contains, which is what merge-base(HEAD,
		// default branch) answers and what the merge itself will diff from.
		base = laneBaseSHA(root)
	}
	if base == "" {
		return Verdict{}, fmt.Errorf("no base to measure %s against", root)
	}
	if isGoModuleRepo(root) {
		return measureGoLane(root, cfg, opts, base, log)
	}
	return measureCargoLane(root, cfg, opts, base, log)
}

// isGoModuleRepo picks the runner. Cargo wins a repo carrying both manifests:
// cargo-mutants measures the crates, and a stray go.mod for a tool directory
// must not silently switch the whole stage over to gremlins.
func isGoModuleRepo(root string) bool {
	return !fileExists(filepath.Join(root, "Cargo.toml")) && fileExists(filepath.Join(root, "go.mod"))
}

// measureCargoLane is the Cargo half: scope, run, re-run the timeouts, judge.
func measureCargoLane(root string, cfg MutantsConfig, opts MeasureOpts, base string, log io.Writer) (Verdict, error) {
	files, crates, err := measureDiff(root, base)
	if err != nil {
		return Verdict{}, err
	}
	if len(files) == 0 {
		return measureSkipped(root, "nothing to measure (0 changed source files)", "nothing-to-measure", log), nil
	}
	jobs, why := measureJobs(opts.Jobs)
	logf(log, "mutants: %d jobs (%s)", jobs, why)
	if v, refused := refuseOnDisk(root, jobs, log); refused {
		return v, nil
	}
	diffPath, err := writeMeasureDiff(root, base, files)
	if err != nil {
		return Verdict{}, err
	}
	excludeFilter, _, bad := mutationBaselineExcludeParse(cfg.BaselineExclude)
	for _, entry := range bad {
		logf(log, "mutants: mutation-baseline-exclude entry refused (needs \"<nextest filter> # reason\"): %q", entry)
	}
	argv := cargoMutantsArgv(MutantsArgv(diffPath, jobs, mutantsMinTestTimeout(root), crates, excludeFilter))
	// An outcomes file left by an EARLIER run describes a different tree. A
	// run that writes none reached no verdict, and the difference between
	// those two is invisible once the stale file is still sitting there.
	_ = os.Remove(cargoMutantsOutcomesPath(root))
	// What the tree looked like before the tool touched it. cargo-mutants
	// mutates in place and restores as it goes, so a run that is killed
	// leaves the last mutation in the source — and merging that is merging a
	// mutant.
	before := snapshotWorktree(root)
	code, runLog, err := runMutantsMeasured(root, measureEnv(root, cfg), argv, log)
	if err != nil {
		return Verdict{}, err
	}
	// The run's own baseline timing, for the NEXT run's timeout budget: a
	// budget derived from a suite that actually ran is the only one that
	// distinguishes a slow test from a mutant that hangs.
	recordMutantsBaseline(root, runLog)
	mutants, err := readCargoMutantsOutcomes(root)
	if err != nil || !cargoMutantsReachedVerdict(code) {
		return measureNoVerdictOrTreeChanged(root, code, err, before, log), nil
	}
	mutants, err = rerunTimedOutMutants(root, cfg, argv, mutants, log)
	if err != nil {
		return Verdict{}, err
	}
	// Asked AFTER the lone re-run as well as after the first run: the re-run
	// is a second in-place mutation pass and can be killed exactly the same
	// way. Ahead of the judgement, because a tree that is no longer the one
	// that was measured makes every count in it a claim about something
	// else.
	if v, refused := refuseIfTreeChanged(root, before, log); refused {
		return v, nil
	}
	return finishMeasure(root, cfg, mutants, log), nil
}

// measureGoLane is the Go half. gremlins is invoked exactly as the detached
// job invoked it, scoped to the same merge base, and its report is read the
// same way.
func measureGoLane(root string, cfg MutantsConfig, opts MeasureOpts, base string, log io.Writer) (Verdict, error) {
	if mutantsGOOSFn() == "windows" {
		// gremlins reports 0.00% mutator coverage here — 4890 mutants NOT
		// COVERED on this repo's own tree. A verdict saying every mutant
		// survived is the wrong answer in the blocking direction, so the
		// stage stands down and says which box would have to measure it.
		return measureSkipped(root, "gremlins-windows", "gremlins-windows", log), nil
	}
	files, err := measureGoDiff(root, base)
	if err != nil {
		return Verdict{}, err
	}
	if len(files) == 0 {
		return measureSkipped(root, "nothing to measure (0 changed source files)", "nothing-to-measure", log), nil
	}
	jobs, why := measureJobs(opts.Jobs)
	logf(log, "mutants: %d jobs (%s)", jobs, why)
	if v, refused := refuseOnDisk(root, jobs, log); refused {
		return v, nil
	}
	out := gremlinsReportPath(root)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return Verdict{}, err
	}
	// The same rule as the Cargo half: an earlier run's report is not this
	// run's, and a run that wrote none reached no verdict.
	_ = os.Remove(out)
	argv := append([]string{gremlinsBin}, gremlinsArgv(base, out, jobs, nil)...)
	// gremlins rewrites the source it mutates too, and is killed by the same
	// things: the tree is snapshotted here for the same reason.
	before := snapshotWorktree(root)
	code, _, err := runMutantsMeasured(root, measureEnv(root, cfg), argv, log)
	if err != nil {
		return Verdict{}, err
	}
	data, readErr := os.ReadFile(out)
	if readErr != nil {
		return measureNoVerdictOrTreeChanged(root, code, readErr, before, log), nil
	}
	mutants, parseErr := parseGremlinsReport(data)
	if parseErr != nil {
		return measureNoVerdictOrTreeChanged(root, code, parseErr, before, log), nil
	}
	if v, refused := refuseIfTreeChanged(root, before, log); refused {
		return v, nil
	}
	return finishMeasure(root, cfg, mutants, log), nil
}

// gremlinsReportPath is where the Go runner writes its machine-readable
// report: under the run's own temp area, never in the tree it is measuring.
func gremlinsReportPath(root string) string {
	return filepath.Join(measureTempDir(root), "gremlins.json")
}

// runMutantsMeasured runs the tool under the box-wide mutation lock, teeing
// its output into the caller's log and returning a copy for the lines this
// side has to read back out of it.
func runMutantsMeasured(root string, env, argv []string, log io.Writer) (int, string, error) {
	var tee strings.Builder
	release := acquireMutantsRunLock("mutants measure for "+root, root)
	code, err := mutantsExecFn(root, env, argv, io.MultiWriter(log, &tee))
	release()
	return code, tee.String(), err
}

// cargoMutantsArgv is the whole command line: cargo-mutants is a cargo
// subcommand, so the program is cargo and the tool is its first argument.
func cargoMutantsArgv(flags []string) []string {
	return append([]string{"cargo", "mutants"}, flags...)
}

// MutantsArgv is cargo-mutants' own flags for a lane measurement, built in
// one place so what the tool is asked to do is one literal a reviewer reads:
// mutate the tree IN PLACE (never a copy), only inside the lane's diff, in
// the order the diff names them, through nextest, with a timeout budget
// derived from the last measured baseline and the box's own job cap.
//
// packages narrows both the mutant pool and the unmutated BASELINE to the
// crates the diff touches; empty falls back to the whole workspace rather
// than measuring nothing. excludeFilter is the repo's own
// mutation-baseline-exclude, already combined into one nextest filterset —
// passed after `--`, which is where cargo-mutants forwards it to the SAME
// test command it runs for both the baseline and every mutant.
func MutantsArgv(diffPath string, jobs, minTestTimeout int, packages []string, excludeFilter string) []string {
	if jobs < 1 {
		jobs = 1
	}
	if minTestTimeout < mutantsMinTestTimeoutFloor {
		minTestTimeout = mutantsMinTestTimeoutFloor
	}
	argv := []string{
		// --no-shuffle: two runs of the same tree must name their mutants in
		// the same order, or a report is not comparable with the one before
		// it.
		"--in-place", "--in-diff", diffPath, "--no-shuffle", "--test-tool=nextest",
		"--minimum-test-timeout", strconv.Itoa(minTestTimeout),
		"--timeout-multiplier", strconv.Itoa(mutantsTimeoutMultiplier),
		"--jobs", strconv.Itoa(jobs),
	}
	for _, pkg := range packages {
		argv = append(argv, "--package", pkg)
	}
	if excludeFilter != "" {
		argv = append(argv, "--", "-E", excludeFilter)
	}
	return argv
}

// mutantsTimeoutMultiplier is how many times the measured baseline a single
// mutant's suite may take before it is called a timeout.
const mutantsTimeoutMultiplier = 3

// measureJobs is the concurrency this run uses and the reason for it. A
// caller that typed a number gets it; everyone else gets the box's own cap.
func measureJobs(want int) (int, string) {
	if want > 0 {
		return want, "flag"
	}
	return mutantsJobsForThisBox()
}

// measureTempDir is where every child of the run writes its temporary files:
// under the target dir, on the build drive, never the system one.
func measureTempDir(root string) string {
	return filepath.Join(ResolveCargoTargetDir(root), "mutants")
}

// measureEnv is the environment the run's children inherit: all three temp
// names pointing at the same directory on the build drive, the switches the
// repo declares its mutation run must set, and the nextest profile it
// declares for them.
func measureEnv(root string, cfg MutantsConfig) []string {
	tmp := measureTempDir(root)
	_ = os.MkdirAll(tmp, 0o755)
	// All three names, on every platform. Setting one and inheriting the
	// others is the bug: whichever the tool reads is the one that decides,
	// and a Windows cargo-mutants that ignored a lone TMPDIR filled C: to
	// 98% with tree copies.
	drop := map[string]bool{"TMPDIR": true, "TMP": true, "TEMP": true, "NEXTEST_PROFILE": true}
	out := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); !ok || !drop[k] {
			out = append(out, kv)
		}
	}
	out = append(out, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
	// Without these, every mutant behind an env-gated suite is missed by
	// construction: 101 of 167 on one lane lived in code only a GPU suite
	// reaches.
	out = append(out, cfg.Env...)
	// The children reach cargo through the queue shim. This marker is the
	// whole handshake: it is what lets `cargo mutants` run at all, and what
	// lets the run build without queueing behind every editor on the box.
	// The call it marks is already inside the box-wide mutation lock, which
	// is the property the queue would otherwise be protecting.
	out = append(out, MutationGateEnv+"="+MutationGateMarked)
	if hasMutantsNextestProfile(root) {
		out = append(out, "NEXTEST_PROFILE="+mutantsNextestProfile)
	}
	return append(out, "CI=1", "NO_COLOR=1")
}

// mutantsNextestProfile is the profile a repo declares for its mutation runs,
// where a slow-timeout tuned for a developer's own box would call a healthy
// suite a hang.
const mutantsNextestProfile = "mutants"

// hasMutantsNextestProfile asks both the repo root and its cargo workspace
// root: a single-crate repo keeps .config/nextest.toml at the root, and a
// workspace keeps it at the workspace root.
func hasMutantsNextestProfile(root string) bool {
	if hasNextestProfile(root, mutantsNextestProfile) {
		return true
	}
	ws := cargoWorkspaceRoot(root)
	return ws != "" && ws != root && hasNextestProfile(ws, mutantsNextestProfile)
}

// refuseOnDisk stops a run that cannot fit its temp copies BEFORE it starts.
// Three runs died at mutant 101 of 131 on a full drive, and every verdict
// they had reached went with them. A drive whose free space cannot be read
// never refuses: this side's own blind spot must not stop a run that would
// have been fine.
func refuseOnDisk(root string, jobs int, log io.Writer) (Verdict, bool) {
	free, ok := freeSpaceGBFn(nearestExistingDir(measureTempDir(root)))
	if !ok {
		return Verdict{}, false
	}
	need := jobs * mutantsDiskPerJobGB
	if free >= need {
		return Verdict{}, false
	}
	msg := fmt.Sprintf("mutants: refused — %d GB free, jobs=%d needs %d GB (%d GB per job); "+
		"a run that fills the drive dies mid-way and takes every verdict with it",
		free, jobs, need, mutantsDiskPerJobGB)
	logf(log, "%s", msg)
	appendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-refused:disk", 0)
	return Verdict{Refused: true, Message: msg}, true
}

// readCargoMutantsOutcomes reads the run's verdicts from the file
// cargo-mutants writes them to, never from its log text.
func readCargoMutantsOutcomes(root string) ([]MutantOutcome, error) {
	data, err := os.ReadFile(cargoMutantsOutcomesPath(root))
	if err != nil {
		return nil, err
	}
	return parseCargoMutantsOutcomes(data)
}

func cargoMutantsOutcomesPath(root string) string {
	return filepath.Join(root, "mutants.out", "outcomes.json")
}

// cargoMutantsLogDir is where cargo-mutants keeps the per-mutant logs that
// explain a run that reached no verdict.
func cargoMutantsLogDir(root string) string {
	return filepath.Join(root, "mutants.out", "log")
}

// cargoMutantsReachedVerdict reports whether an exit status is one
// cargo-mutants uses to describe a completed run: 0 (all caught), 2 (missed)
// or 3 (timeout). Anything else means the run stopped, and a stopped run's
// silence must never read as "nothing survived".
func cargoMutantsReachedVerdict(code int) bool {
	return code == 0 || code == 2 || code == 3
}

// rerunTimedOutMutants re-runs every timed-out mutant once, alone. Nine
// timeouts on one lane were all contention, and a refusal that names
// contention as a survivor is a false report; one that times out again with
// the box to itself stays unmeasured, which is not the same as caught.
func rerunTimedOutMutants(root string, cfg MutantsConfig, argv []string, mutants []MutantOutcome, log io.Writer) ([]MutantOutcome, error) {
	var names []string
	for _, m := range mutants {
		if m.Status == "timeout" {
			names = append(names, outcomeName(m))
		}
	}
	if len(names) == 0 {
		return mutants, nil
	}
	logf(log, "mutants: re-running %d timed-out mutant(s) alone", len(names))
	rerun, err := runMutantsMeasuredRerun(root, cfg, argv, names, log)
	if err != nil {
		return nil, err
	}
	settled := map[string]string{}
	for _, m := range rerun {
		settled[outcomeName(m)] = m.Status
	}
	for i, m := range mutants {
		if status, ok := settled[outcomeName(m)]; ok && m.Status == "timeout" {
			mutants[i].Status = status
		}
	}
	return mutants, nil
}

// runMutantsMeasuredRerun is the lone re-run: the same argv with the
// concurrency forced to one and a name filter naming only the mutants under
// re-examination.
func runMutantsMeasuredRerun(root string, cfg MutantsConfig, argv, names []string, log io.Writer) ([]MutantOutcome, error) {
	rerun := withJobsOne(argv)
	rerun = insertBeforePassthrough(rerun, []string{"--re", mutantsNameFilter(names)})
	if _, _, err := runMutantsMeasured(root, measureEnv(root, cfg), rerun, log); err != nil {
		return nil, err
	}
	out, err := readCargoMutantsOutcomes(root)
	if err != nil {
		// The re-run wrote nothing readable, so it settled nothing: the
		// mutants stay timed out and the merge is refused naming them.
		logf(log, "mutants: the lone re-run left no readable outcomes: %v", err)
		return nil, nil
	}
	return out, nil
}

// withJobsOne rewrites --jobs to 1: the whole point of the re-run is that the
// mutant has the box to itself.
func withJobsOne(argv []string) []string {
	out := append([]string{}, argv...)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "--jobs" {
			out[i+1] = "1"
		}
	}
	return out
}

// insertBeforePassthrough puts flags before the `--` that hands the rest to
// nextest, so a re-run's own flags never end up as test-tool arguments.
func insertBeforePassthrough(argv, flags []string) []string {
	for i, a := range argv {
		if a == "--" {
			out := append([]string{}, argv[:i]...)
			out = append(out, flags...)
			return append(out, argv[i:]...)
		}
	}
	return append(append([]string{}, argv...), flags...)
}

// mutantsNameFilter is the anchored regex selecting exactly the named
// mutants and nothing else — the same `^…$` shape the resumed run's own
// --exclude-re uses.
func mutantsNameFilter(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, regexp.QuoteMeta(n))
	}
	if len(quoted) == 1 {
		return "^" + quoted[0] + "$"
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// outcomeName is the tool's own spelling of a mutant, rebuilt from its parts
// when the producer reported them separately.
func outcomeName(m MutantOutcome) string {
	if m.Name != "" {
		return m.Name
	}
	return mutantLineOf(m.File, m.Line, m.Col, m.Mutation)
}
