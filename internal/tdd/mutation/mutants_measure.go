package mutation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
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
// and reports what it found. There is no store, no carry and no document: with
// the measurement and the judgement collapsed into one event there is nothing
// left for a cache to carry between them.

// MeasureOpts is what the caller knows that the repo's config does not.
// There is no Jobs: both halves derive their concurrency from the box they
// run on — the Cargo half as a shard count, the Go half as gremlins' worker
// count — and neither takes a number from a caller.
type MeasureOpts struct {
	// Ctx is the caller's patience. Cancelling it kills the tool's whole
	// process tree and releases the box-wide mutation-run lock, so a caller
	// that gives up leaves nothing behind for the next one to wait on. nil
	// means context.Background(): the gate's own runs wait as long as the
	// measurement takes.
	Ctx  context.Context
	Base string    // sha or ref the lane is measured against
	Log  io.Writer // the run's own narrative; never the verdict
	// ReportOut is where to publish what this run measured, for a box whose
	// own runner cannot measure the same tree (mutants_runner.go). Empty
	// publishes nothing, which is every local run: only the CI job that
	// measures on behalf of another box passes it.
	ReportOut string
}

// mutantsExecFn runs one mutation tool and reports its exit code. A seam, the
// same shape as mutantsProducerFn: a test proves what the runner decides
// without a toolchain on the box.
var mutantsExecFn = runMutantsTool

// runMutantsTool is the real spawn. argv[0] is the program — `cargo` for a
// Cargo lane, `gremlins` for a Go one — so one seam covers both runners.
//
// ctx is the caller's own patience, and it reaches the whole process TREE.
// cargo-mutants is a launcher: it spawns cargo, which spawns rustc and
// nextest, and the Cancel exec.CommandContext installs by default kills only
// the pid it started. A caller that gave up on a run whose children kept
// compiling would still be holding the box-wide mutation-run lock they
// inherited, so the next measurement waits a year for a run nobody is
// reading — which is exactly the hang the deadline existed to prevent.
// cmd.Cancel is therefore proc.KillTree (the same one a deferred build phase
// uses) and WaitDelay bounds how long Wait stays for the output pipes to
// drain after the kill.
func runMutantsTool(ctx context.Context, dir string, env []string, argv []string, log io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = suiteAttrs()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return proc.KillTree(cmd.Process.Pid)
	}
	cmd.WaitDelay = mutantsKillDrainDelay
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// mutantsKillDrainDelay is how long Wait stays after the kill for the
// output pipes to drain. A mutation run's children write megabytes; a
// killed tree that still holds the pipe must not hold the caller too.
const mutantsKillDrainDelay = 5 * time.Second

// SetMutantsExecForTest replaces that seam for one test and answers the
// restore. Exported because what the CLI layer does with a verdict — the
// report it prints, the stream it prints it on, the exit code it turns it
// into — is only provable from the package that owns the command, and no box
// running that test has a mutation tool installed.
func SetMutantsExecForTest(fn func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error)) (restore func()) {
	prev := mutantsExecFn
	mutantsExecFn = fn
	return func() { mutantsExecFn = prev }
}

// mutantsGOOSFn names the platform the stage judges itself on, a seam so the
// Windows stand-down can be proved on any box.
var mutantsGOOSFn = func() string { return runtime.GOOS }

// SetMutantsGOOSForTest forces the platform for the duration of a test.
func SetMutantsGOOSForTest(goos string) (restore func()) {
	prev := mutantsGOOSFn
	mutantsGOOSFn = func() string { return goos }
	return func() { mutantsGOOSFn = prev }
}

// MeasureLane measures root's changes against opts.Base (sharded
// cargo-mutants, or gremlins), judges them against the repo's
// mutation-accept, runs mutants-after, and returns the verdict. A verdict is
// never an error; an error is the runner failing to start.
func MeasureLane(root string, cfg MutantsConfig, opts MeasureOpts) (Verdict, error) {
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
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
		return measureGoLane(ctx, root, cfg, base, opts.ReportOut, log)
	}
	return measureCargoLane(ctx, root, cfg, base, log)
}

// isGoModuleRepo picks the runner. Cargo wins a repo carrying both manifests:
// cargo-mutants measures the crates, and a stray go.mod for a tool directory
// must not silently switch the whole stage over to gremlins.
func isGoModuleRepo(root string) bool {
	return !fileExists(filepath.Join(root, "Cargo.toml")) && fileExists(filepath.Join(root, "go.mod"))
}

// measureCargoLane is the Cargo half: scope, run, re-run the timeouts, judge.
func measureCargoLane(ctx context.Context, root string, cfg MutantsConfig, base string, log io.Writer) (Verdict, error) {
	files, crates, err := measureDiff(root, base)
	if err != nil {
		return Verdict{}, err
	}
	if len(files) == 0 {
		return measureSkipped(root, "nothing to measure (0 changed source files)", "nothing-to-measure", log), nil
	}
	// One cargo-mutants process per SHARD, side by side: each copies the
	// source tree, mutates its own copy and builds into a persistent target
	// directory of its own (mutants_shards.go), so the checkout is never
	// touched. In-place (one tree, one job) measured 739 mutants in 16 h on
	// a box that could run eight copies; the number the box derives is how
	// many shards the mutant pool is divided into, and refuseOnDisk fits that
	// number to what the build drive measures.
	shards, why := mutantsJobsForThisBoxFn(mutantsCargoShardGB)
	// Before the drive has its say, because a repo that has lowered the count
	// needs less of everything the budgets below measure.
	shards, why = capShardsToConfig(cfg, shards, why)
	v, shards, refused := refuseOnDisk(root, shards, "shard", log)
	if refused {
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
	argv := cargoMutantsArgv(MutantsArgv(diffPath, mutantsMinTestTimeout(root), crates, excludeFilter))
	// The last thing that lowers the count, after the drive has had its say:
	// a shard with no mutants in its slice still pays a cold baseline build to
	// report nothing.
	shards, why = capShardsToMutants(ctx, root, cfg, argv, shards, why, log)
	logf(log, "mutants: %d shards (%s)", shards, why)
	// The build width is said out loud too, in the same shape, but by
	// runMutantsShards: it depends on whether the shards start warm, which is
	// only settled once the run holds the box-wide lock and has done whatever
	// warming it is going to do.
	//
	// What the tree looked like before the tool touched it. cargo-mutants
	// mutates its copy, but a run that is killed part-way can still leave a
	// mutation in the source it copied from — and merging that is merging a
	// mutant.
	before := snapshotWorktree(root)
	runs, err := runMutantsShards(ctx, root, cfg, argv, shards, log)
	if err != nil {
		return Verdict{}, err
	}
	// The run's own baseline timing, for the NEXT run's timeout budget: a
	// budget derived from a suite that actually ran is the only one that
	// distinguishes a slow test from a mutant that hangs. Every shard runs
	// its own baseline and calibrates its own mutant timeout from it; what
	// is recorded here is the first shard's, in shard order.
	recordMutantsBaseline(root, shardLogText(runs))
	mutants, code, logDir, cause := mergeShardOutcomes(root, runs)
	if cause != nil {
		return measureNoVerdictOrTreeChanged(root, logDir, code, cause, before, log), nil
	}
	mutants, err = rerunTimedOutMutants(ctx, root, cfg, argv, mutants, log)
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
func measureGoLane(ctx context.Context, root string, cfg MutantsConfig, base, reportOut string, log io.Writer) (Verdict, error) {
	if mutantsGOOSFn() == "windows" {
		// gremlins reports 0.00% mutator coverage here — 4890 mutants NOT
		// COVERED on this repo's own tree — so the run does not happen on
		// this box. It happens on the self-hosted Linux runner instead, and
		// what arrives here is that measurement, consumed only when it is a
		// measurement of the exact tree this gate is judging
		// (mutants_runner.go). When none is, the outcome is the gap issue
		// #699 built: inconclusive, and blocking nothing.
		return measureOnRunner(root, cfg, log), nil
	}
	files, err := measureGoDiff(root, base)
	if err != nil {
		return Verdict{}, err
	}
	if len(files) == 0 {
		return measureSkipped(root, "nothing to measure (0 changed source files)", "nothing-to-measure", log), nil
	}
	// gremlins copies nothing into the tree it measures and takes a worker
	// count happily, so the Go half keeps the box's own cap. Its workers are
	// priced on the drive by mutants_gobudget.go, not as Cargo shards.
	jobs, why := mutantsJobsForThisBoxFn(mutantsGoJobGB)
	logf(log, "mutants: %d jobs (%s)", jobs, why)
	v, jobs, refused := refuseOnDisk(root, jobs, "job", log)
	if refused {
		return v, nil
	}
	out := gremlinsReportPath(root)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return Verdict{}, err
	}
	// A mutant's own test process is killed outright on failfast or a
	// timeout, never reaching the TestMain cleanup that made its temp
	// directory — measureEnv points TMPDIR straight at this area, so that
	// directory lands and stays here. Swept before this run starts, so
	// whatever the box killed before its own turn finished does not survive
	// into this one, and again (deferred) once this run is done, so its own
	// dead children do not survive into the next.
	sweepGoMeasureTemp(root)
	defer sweepGoMeasureTemp(root)
	// The same rule as the Cargo half: an earlier run's report is not this
	// run's, and a run that wrote none reached no verdict.
	_ = os.Remove(out)
	argv := append([]string{gremlinsBin}, gremlinsArgv(base, out, jobs, nil)...)
	// gremlins rewrites the source it mutates too, and is killed by the same
	// things: the tree is snapshotted here for the same reason.
	before := snapshotWorktree(root)
	code, _, err := runMutantsMeasured(ctx, root, measureEnv(root, cfg), argv, log)
	if err != nil {
		return Verdict{}, err
	}
	// The Go runner keeps no per-mutant log directory, so a refusal points
	// at the area it writes its report into rather than at a cargo-mutants
	// layout it never produces.
	data, readErr := os.ReadFile(out)
	if readErr != nil {
		return measureNoVerdictOrTreeChanged(root, measureTempDir(root), code, readErr, before, log), nil
	}
	mutants, parseErr := parseGremlinsReport(data)
	if parseErr != nil {
		return measureNoVerdictOrTreeChanged(root, measureTempDir(root), code, parseErr, before, log), nil
	}
	if v, refused := refuseIfTreeChanged(root, before, log); refused {
		return v, nil
	}
	// gremlins judged each mutant with its own package's tests; which of
	// those verdicts that selection could actually have reached is a
	// question about the MODULE, answered here in one `go list` for the
	// whole run (issue #695).
	outcomes := classifyGoSurvivorReach(root, mutants)
	// Published before the judging, not after: what another box needs is the
	// OUTCOMES, judged there against the accept-list that lives in the tree
	// this measurement is bound to.
	writeRunnerReport(root, reportOut, base, outcomes, log)
	return finishMeasure(root, cfg, outcomes, log), nil
}

// gremlinsReportPath is where the Go runner writes its machine-readable
// report: under the run's own temp area, never in the tree it is measuring.
func gremlinsReportPath(root string) string {
	return filepath.Join(measureTempDir(root), "gremlins.json")
}

// sweepGoMeasureTemp clears the Go runner's own measurement area of
// everything except gremlins' own report. Unlike the sharded Cargo runner,
// gremlins keeps no persistent build directory here to protect — the whole
// area is TMPDIR for the run (measureEnv), and gremlins "copies nothing into
// the tree it measures" (measureGoLane) — so there is no ownership or
// liveness question to ask: anything found here besides the report is a
// killed mutant's orphaned temp directory, always, and is reclaimed
// unconditionally. A missing area is not swept; there is nothing in it.
func sweepGoMeasureTemp(root string) {
	area := measureTempDir(root)
	entries, err := os.ReadDir(area)
	if err != nil {
		return
	}
	keep := filepath.Base(gremlinsReportPath(root))
	for _, e := range entries {
		if e.Name() == keep {
			continue
		}
		_ = os.RemoveAll(filepath.Join(area, e.Name()))
	}
}

// runMutantsMeasured runs the tool under the box-wide mutation lock, teeing
// its output into the caller's log and returning a copy for the lines this
// side has to read back out of it.
func runMutantsMeasured(ctx context.Context, root string, env, argv []string, log io.Writer) (int, string, error) {
	var tee strings.Builder
	release := acquireMutantsRunLock("mutants measure for "+root, root)
	waitForCIRunnerJobs(ctx, root, log)
	code, err := mutantsExecFn(ctx, root, env, argv, io.MultiWriter(log, &tee))
	release()
	return code, tee.String(), err
}

// mutantsTimeoutMultiplier is how many times the measured baseline a single
// mutant's suite may take before it is called a timeout.
const mutantsTimeoutMultiplier = 3

// measureTempDir is the run's own area on the build drive, never the system
// one: the scoped diff, one output directory per shard, and the persistent
// per-shard target directories that make the next run warm.
func measureTempDir(root string) string {
	// Beside the checkout, never inside it: a temp dir under <root> is a
	// directory the source-tree copy would copy into itself, and a target
	// dir under <root>/target is one an editor's own build shares. The
	// parent of the worktree keeps all of it on the same disk as the lane,
	// next to it, and out of anything cargo or git would walk.
	return filepath.Join(filepath.Dir(root), ".mutants", filepath.Base(root))
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
	drop := map[string]bool{"TMPDIR": true, "TMP": true, "TEMP": true, "NEXTEST_PROFILE": true, "CARGO_TARGET_DIR": true}
	out := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); !ok || !drop[k] {
			out = append(out, kv)
		}
	}
	out = append(out, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
	// No CARGO_TARGET_DIR here, and never the LANE's: an editor's slotted
	// build owns that directory behind cargo's own blocking lock, and a
	// measurement that took it would hold it for hours. measureShardEnv
	// gives each shard a persistent directory of its own instead. Distinct
	// per shard is the load-bearing part — ONE dir shared by N processes
	// would put them behind that same build-directory lock one after
	// another, which is the serial run the shards exist to end.
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
