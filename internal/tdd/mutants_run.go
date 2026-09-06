package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The detached half of a mutation job: prepare the warm worktree, work out
// which files the run actually has to measure, hand the producer its flags,
// and fold the carried outcomes into whatever receipt the producer wrote.
//
// The producer is the consuming repo's own runner (borld:
// tools/mutation_gate.sh), because only that repo knows which mutants are
// worth generating and which survivors it has accepted. What this side owns
// is everything that is the same in every language: where the run happens,
// what it may skip, and that the answer ends up in one signed receipt.

// mutantsEnv are the variables the producer reads. They are a contract with
// the consuming repo's script — docs/mutation-runner.md is the spec, and a
// script that has not caught up simply ignores them and does a full run.
const (
	// MutationGateEnv marks a mutation run started through the gate. The
	// cargo shim refuses a bare `cargo mutants` without it.
	MutationGateEnv = "MUTATION_GATE"
	// QueueEnv=bypass lets this run past the build queue, honoured ONLY when
	// the target dir is the mutation worktree's own (see the cargo shim).
	QueueEnv = "APHROLLO_QUEUE"
	// QueueBypass is the one value that means "do not queue".
	QueueBypass = "bypass"
	// MutantsArgsEnv carries the cargo-mutants flags this side computed.
	MutantsArgsEnv = "APHROLLO_MUTANTS_ARGS"
	// MutantsDiffEnv names the reduced lane diff the run is scoped to.
	MutantsDiffEnv = "APHROLLO_MUTANTS_DIFF"
	// MutantsBaseEnv is the base sha the receipt must record.
	MutantsBaseEnv = "APHROLLO_MUTANTS_BASE"
	// MutantsEnvEnv carries the switches the workspace declares its mutation
	// run must set, so an env-gated suite counts.
	MutantsEnvEnv = "APHROLLO_MUTANTS_ENV"
	// MutantsJobsEnv is the concurrency cap this box allows, and
	// MutantsJobsWhyEnv the reason a runner prints beside it. Read as a
	// session-wide override by resolveMutantsJobs, beaten only by a `--jobs`
	// flag typed for the one run.
	MutantsJobsEnv    = "APHROLLO_MUTANTS_JOBS"
	MutantsJobsWhyEnv = "APHROLLO_MUTANTS_JOBS_WHY"
	// MutantsBaseOverrideEnv, MutantsTimeoutMultiplierEnv and
	// MutantsMinTestTimeoutEnv carry `aphrollo gate mutants run`'s own
	// `--base`/`--timeout-multiplier`/`--minimum-test-timeout` flags into the
	// producer: set on THIS process by the CLI layer before the job runs,
	// read here, and never forwarded raw to the producer's own environment.
	MutantsBaseOverrideEnv      = "APHROLLO_MUTANTS_BASE_OVERRIDE"
	MutantsTimeoutMultiplierEnv = "APHROLLO_MUTANTS_TIMEOUT_MULTIPLIER"
	MutantsMinTestTimeoutEnv    = "APHROLLO_MUTANTS_MIN_TEST_TIMEOUT"
)

// runMutantsJob measures one described job. It never blocks a commit, so a
// finished run exits 0 whatever the verdict -- what it has to say goes in the
// job's log and in gate.log. An ERROR that stopped it measuring at all is the
// exception and exits non-zero: a caller that asked for a run and got none is
// owed the difference.
//
// For the detached wrapper, log is this process's own stdout, which the parent
// redirected to the job's log file at spawn; for a hand-typed foreground run
// it is the session's terminal.
func runMutantsJob(j MutantsJob, log io.Writer) int {
	// A queued job can wait long enough for its lane to be reset, amended,
	// rebased or squash-merged, or for gc to reclaim an orphaned commit — not
	// a worktree fault, so it is checked and dropped before
	// prepareMutantsWorktree touches disk (issue #367).
	if !mutantsTipResolvable(j.RepoRoot, j.Tip) {
		logf(log, "aphrollo: lane tip %s no longer resolves in %s — the lane was rewritten after this job was recorded; dropping it", short(j.Tip), j.RepoRoot)
		recordMutantsTipRewritten(j)
		return 0
	}
	if err := prepareMutantsWorktree(j); err != nil {
		logf(log, "aphrollo: could not prepare %s: %v", j.Worktree, err)
		appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-worktree-failed", 0)
		return 1
	}
	// Printed on EVERY run, whatever it goes on to measure: a run that found
	// nothing because its scope matched no files and a run that found nothing
	// because the worktree it reused was not the tree it assumed both read
	// identically otherwise, and the difference was destroyed the moment a
	// later run re-prepared the same directory (issue #283).
	logf(log, "aphrollo: mutants worktree %s is at %s (job tip %s)", j.Worktree, strings.TrimSpace(gitOut(j.Worktree, "rev-parse", "HEAD")), j.Tip)
	plan, carried, now, skipped := scopeMutantsRun(j)
	// Whatever an interrupted attempt on this same tree already reached: those
	// mutants are excluded from this run and their verdicts kept.
	judged := loadMutantsPartials(j.TipTree)
	if len(plan) == 0 && len(carried) == 0 {
		logf(log, "aphrollo: nothing mutable in this lane's diff")
		appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-nothing-to-do", 0)
		return 0
	}
	// Move-aware: git's own move detection decides which changed lines are
	// only relocated code, and those never reach the runner.
	laneDiff, movedLines := moveAwareDiff(j.RepoRoot, j.BaseSHA, j.Tip, plan)
	if len(plan) == 0 || !diffHasHunks(laneDiff) {
		// The cache answers for every file this lane touched. Measuring
		// nothing is the whole point of the cache — and with no files to
		// scope it to, `git diff BASE TIP --` would be the FULL lane diff,
		// so the run would re-measure exactly what was just excluded.
		r := writeCarriedReceipt(j, append(append([]MutantOutcome{}, carried...), judged...), movedLines)
		verdict := "mutants-fully-carried:" + short(j.TipTree)
		if movedLines > 0 && r.MutantsTotal == 0 {
			verdict = "mutants-all-moved:" + short(j.TipTree)
		}
		appendGateLog("mutants", logToken(j.Repo), "mutants", verdict, 0)
		logf(log, "aphrollo: nothing left to measure in this lane (%d moved line(s), %d outcome(s) carried); receipt written",
			movedLines, r.MutantsTotal)
		return 0
	}
	if err := os.WriteFile(j.Diff, []byte(laneDiff), 0o600); err != nil {
		logf(log, "aphrollo: could not write the lane diff: %v", err)
		return 1
	}
	logf(log, "aphrollo: %d file(s) to measure, %d carried, %d already judged by an interrupted attempt, %d moved line(s) skipped",
		len(plan), len(carried), len(judged), movedLines)
	// Why each file has to be paid for again. A run that re-measures a file
	// the lane never edited is the expensive case, and without this the log
	// gave no way to tell it from an ordinary first measurement.
	for _, line := range CarrySkipSummary(skipped) {
		logf(log, "aphrollo: %s", line)
	}

	start := time.Now()
	// The box-wide lock covers the WHOLE producer call, its own cold build
	// included: several lanes building the same crates at once OOM'd rustc
	// mid-build, the identical resource argument as the thread starvation
	// issue #253 reports for the test phase. Held only for this call —
	// acquired immediately before, released immediately after — never
	// across the bookkeeping below.
	releaseRunLock := acquireMutantsRunLock("mutants run for "+j.Repo, j.RepoRoot)
	code := mutantsProducerFn(j, judged)
	releaseRunLock()
	// Read BEFORE judging the exit code: a run that died at mutant 101 of 131
	// still wrote 100 verdicts, and this is the read that stops them being
	// thrown away (issue #103).
	saveMutantsPartials(j.TipTree, readMutantsOut(j.Worktree))
	appendGateLog("mutants", logToken(j.Repo), "mutants", mutantsVerdict(code)+":"+short(j.TipTree), time.Since(start))
	if code == 0 {
		clearMutantsDeath(j.TipTree)
		adoptCarriedOutcomes(j, append(append([]MutantOutcome{}, carried...), judged...), now)
		// Every finished run feeds the shared cache, which is what makes the
		// NEXT lane over these blobs cheap.
		if r, ok := readReceiptFile(MutationReceiptPathFor(j.TipTree)); ok {
			MergeMutantStore(j.Repo, r.Outcomes)
		}
		return 0
	}
	recordMutantsDeath(j, code, mutantsDeathTail(j))
	return 0
}

// mutantNames renders the mutants an interrupted attempt already judged, in
// the spelling the TOOL names them by — verbatim where it was read from
// mutants.out, rebuilt with the column where a producer reported parts. A
// rebuilt name that drops the column matches no mutant, which is how the first
// version of this excluded nothing at all.
func mutantNames(judged []MutantOutcome) []string {
	out := make([]string, 0, len(judged))
	for _, m := range judged {
		if m.Name != "" {
			out = append(out, m.Name)
			continue
		}
		out = append(out, mutantLineOf(m.File, m.Line, m.Col, m.Mutation))
	}
	return out
}

func mutantsVerdict(code int) string {
	if code == 0 {
		return "mutants-finished"
	}
	return "mutants-failed"
}

// mutantsTipResolvable answers true when the tip resolves OR the question is
// inconclusive (git failed to even run) — only a clean git invocation that
// explicitly reports the object missing counts as a confirmed rewrite.
func mutantsTipResolvable(repoRoot, tip string) bool {
	_, err := git(repoRoot, "cat-file", "-e", tip+"^{commit}")
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	return !errors.As(err, &exitErr)
}

// prepareMutantsWorktree checks the dedicated worktree out at the tip,
// creating it the first time. It resets tracked files (the previous run
// mutated them in place) and deliberately does NOT clean untracked ones: the
// warm target dir lives in there, and deleting it is the cold build this
// whole design exists to avoid.
func prepareMutantsWorktree(j MutantsJob) error {
	if j.Worktree == "" || j.Tip == "" {
		return errors.New("job names no worktree")
	}
	if _, err := os.Stat(filepath.Join(j.Worktree, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(j.Worktree), 0o755); err != nil {
			return err
		}
		// Before adding one more warm tree, drop the ones whose lanes are
		// gone: each carries a target dir, and the run refuses to start below
		// 15 GB free per job.
		reclaimStaleMutantsLanes(j.RepoRoot)
		if out, err := git(j.RepoRoot, "worktree", "add", "--detach", j.Worktree, j.Tip); err != nil {
			return errors.New(strings.TrimSpace(out))
		}
		writeMutantsLaneMarker(j.Worktree, j.RepoRoot)
		return nil
	}
	if out, err := git(j.Worktree, "reset", "-q", "--hard", j.Tip); err != nil {
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

// mutantsProducerFn is the producer, a seam so a run's own decisions can be
// tested without a toolchain.
var mutantsProducerFn = runMutantsProducer

// runMutantsProducer runs the consuming repo's own mutation runner in the
// warm worktree, with the environment that tells it where to build, what to
// mutate and that it is allowed past the build queue.
func runMutantsProducer(j MutantsJob, judged []MutantOutcome) int {
	argv, ok := mutantsProducerArgv(j)
	if !ok {
		logf(os.Stdout, "aphrollo: no mutation runner in %s (expected tools/mutation_gate.sh)", j.Worktree)
		return 1
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = j.Worktree
	cmd.Env = mutantsChildEnv(j, judged)
	// The producer inherits this process's own streams, which the parent
	// pointed at the job's log files: one place to look, whether the run
	// finished or died.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		logf(os.Stdout, "aphrollo: %v", err)
		return 1
	}
	return 0
}

// mutantsProducerArgv picks the runner the repo actually has. The Rust runner
// is the repo's own script, because the accept-list and the crate scoping are
// its to decide.
func mutantsProducerArgv(j MutantsJob) ([]string, bool) {
	if script := filepath.Join(j.Worktree, "tools", "mutation_gate.sh"); fileExists(script) {
		return []string{"bash", filepath.ToSlash(script), effectiveMutantsBase(j)}, true
	}
	if fileExists(filepath.Join(j.Worktree, "go.mod")) {
		self, err := os.Executable()
		if err != nil {
			return nil, false
		}
		return []string{self, CmdName, "mutants", "go", "--job", mutantsJobFilePath(j)}, true
	}
	return nil, false
}

// effectiveMutantsBase is j.BaseSHA unless `aphrollo gate mutants run` was
// typed with `--base <ref>`, which the CLI layer carries in
// MutantsBaseOverrideEnv on this process — the job file describes what
// postcommit scoped the run to, and a hand-typed rerun against a different
// base is the one case that legitimately overrides it.
func effectiveMutantsBase(j MutantsJob) string {
	if o := strings.TrimSpace(os.Getenv(MutantsBaseOverrideEnv)); o != "" {
		return o
	}
	return j.BaseSHA
}

// mutantsChildEnv is the producer's environment: the warm target dir, the
// queue bypass that pairs with it, the computed flags, and the marker that
// says this run came through the gate.
func mutantsChildEnv(j MutantsJob, judged []MutantOutcome) []string {
	out := make([]string, 0, len(os.Environ())+8)
	drop := map[string]bool{
		"CARGO_TARGET_DIR": true, MutationGateEnv: true, QueueEnv: true,
		// All three, always: the runner that set only TMPDIR left a Windows
		// cargo-mutants writing tree copies to C: until the drive was 98% full.
		"TMPDIR": true, "TMP": true, "TEMP": true,
		MutantsArgsEnv: true, MutantsDiffEnv: true, MutantsBaseEnv: true,
		MutantsEnvEnv: true, MutantsJobsEnv: true, MutantsJobsWhyEnv: true,
		BuildLockHeldEnv:       true,
		MutantsBaseOverrideEnv: true, MutantsTimeoutMultiplierEnv: true, MutantsMinTestTimeoutEnv: true,
		MutantsBaselineExcludedEnv: true,
	}
	for _, kv := range os.Environ() {
		// Every GIT_* variable goes, the way cleanGitEnv already drops them
		// for this package's own git calls. A detached job is spawned from a
		// post-commit hook, which git runs with GIT_DIR and GIT_INDEX_FILE
		// set to absolute paths inside the LANE's git dir; git reads those
		// before it looks at the directory it was run in, so the run's own
		// isolated tree bought nothing while they rode along (issue #156).
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		if k, _, ok := strings.Cut(kv, "="); !ok || !drop[k] {
			out = append(out, kv)
		}
	}
	// Env-versus-flag: a `--jobs` flag typed for this run set MutantsJobsEnv
	// on this very process just before the job ran, so resolveMutantsJobs
	// reads it as the override — indistinguishable, by design, from a
	// session that set it for the same reason.
	jobs, why := resolveMutantsJobs(0, false)
	// cargoWorkspaceRoot answers j.Worktree when it finds no workspace table,
	// so a single-crate repo's own Cargo.toml is what gets read here.
	ws := cargoWorkspaceRoot(j.Worktree)
	excludeFilter, excludedCount := mutationBaselineExcludeForRun(ws, os.Stdout)
	argv := MutantsArgv(j.Diff, TipSuiteGreen(j.RepoRoot, j.Started.Add(-mutantsGreenWindow)), mutantNames(judged), mutantsTouchedPackages(j), excludeFilter)
	// `--timeout-multiplier`/`--minimum-test-timeout` are cargo-mutants' own
	// flags; a run typed with either rides through unchanged rather than
	// through the exclusion-list budget MutantsArgv already bounds.
	if tm := strings.TrimSpace(os.Getenv(MutantsTimeoutMultiplierEnv)); tm != "" {
		argv = append(argv, "--timeout-multiplier", tm)
	}
	if mt := strings.TrimSpace(os.Getenv(MutantsMinTestTimeoutEnv)); mt != "" {
		argv = append(argv, "--minimum-test-timeout", mt)
	}
	out = append(out, mutantsTempEnv(j)...)
	return append(out,
		"CARGO_TARGET_DIR="+j.TargetDir,
		MutationGateEnv+"=1",
		QueueEnv+"="+QueueBypass,
		MutantsArgsEnv+"="+strings.Join(argv, " "),
		MutantsDiffEnv+"="+j.Diff,
		MutantsBaseEnv+"="+effectiveMutantsBase(j),
		// The switches the repo says its mutation run must set: without them
		// every mutant behind an env-gated suite is missed by construction.
		MutantsEnvEnv+"="+strings.Join(cargoMutantsEnv(ws), " "),
		MutantsJobsEnv+"="+strconv.Itoa(jobs),
		MutantsJobsWhyEnv+"="+why,
		MutantsBaselineExcludedEnv+"="+strconv.Itoa(excludedCount),
		"CI=1", "NO_COLOR=1")
}

// mutantsGreenWindow is how far back a green commit-gate run still counts as
// this tip's. The gate runs seconds before the commit it lets through; half
// an hour is slack for a slow suite, not a second lane's answer.
const mutantsGreenWindow = 30 * time.Minute

// adoptCarriedOutcomes folds the outcomes this run inherited into the receipt
// the producer wrote, so the receipt describes the WHOLE lane diff rather than
// only the part that was re-measured — and so the next run has something to
// carry in turn. now is the gate's own reading of this job's tip
// (scopeMutantsRun), and re-stamps every outcome's blob, fence and package
// before anything reaches the shared store: the worktree was reset to this
// exact tip before the producer ever ran, so that reading is the one true
// measurement of what every outcome here was actually taken against. A
// producer's own self-reported blob is unverified, and trusting it let a
// wrong or stale claim enter the repo-wide store and later match some OTHER
// tree's real blob by coincidence — the false carry issue #336 reports.
func adoptCarriedOutcomes(j MutantsJob, carried []MutantOutcome, now TreeState) {
	path := MutationReceiptPathFor(j.TipTree)
	if path == "" {
		return
	}
	r, data, ok := readReceiptFileRaw(path)
	if !ok {
		return
	}
	outcomesFieldPresent := receiptHasOutcomesField(data)
	have := map[mutantKey]bool{}
	for _, m := range r.Outcomes {
		have[m.key()] = true
	}
	for _, m := range carried {
		if !have[m.key()] {
			r.Outcomes = append(r.Outcomes, m)
		}
	}
	r.Outcomes = stampTreeState(r.Outcomes, now, mutantsProducerVersion(j.Worktree))
	recountReceipt(&r, outcomesFieldPresent)
	// The producer already signed r before this ran; the merge just changed
	// its body, which leaves the old mac describing outcomes that are no
	// longer there. Re-signed here, the same way a carried receipt written
	// elsewhere (writeCarriedReceipt) already is — an unsigned or stale-MAC
	// receipt is refused at merge exactly the same as a hand-written one.
	signReceipt(&r)
	writeReceiptFile(path, r)
}

// writeCarriedReceipt is the receipt for a run that measured nothing: either
// the cache already answered for every file the lane touched, or every line
// the lane changed was one git judged to have MOVED. It is the same document a
// measured run writes, signed the same way — a merge reads one shape, whatever
// produced it — and it records the moved-line count, so a zero-mutant receipt
// says why it is zero.
func writeCarriedReceipt(j MutantsJob, carried []MutantOutcome, movedLines int) MutationReceipt {
	path := MutationReceiptPathFor(j.TipTree)
	if path == "" {
		return MutationReceipt{}
	}
	r := MutationReceipt{
		Repo: j.Repo, RepoID: j.RepoID, Branch: j.Branch, TipTree: j.TipTree,
		BaseRef: j.BaseRef, BaseSHA: j.BaseSHA,
		Verdict: receiptVerdictPass, FinishedAt: time.Now().UTC(),
		Outcomes:   carried,
		MovedLines: movedLines,
		Files:      map[string]string{}, Fences: map[string]string{},
	}
	for _, m := range carried {
		if m.Blob != "" {
			r.Files[m.File] = m.Blob
		}
		if m.Fence != "" {
			r.Fences[m.Package] = m.Fence
		}
	}
	recountReceipt(&r, true)
	signReceipt(&r)
	writeReceiptFile(path, r)
	return r
}

// spawnMutantsJob starts the wrapper detached and below normal priority, and
// returns its pid.
func spawnMutantsJob(j MutantsJob) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	path := mutantsJobFilePath(j)
	if err := writeMutantsJobFile(path, j); err != nil {
		return 0, err
	}
	cmd := exec.Command(self, CmdName, "mutants", "run", "--job", path)
	cmd.Dir = j.RepoRoot
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1")
	// FILES, not the null device: a detached run has nowhere to print, and a
	// run that dies must leave the reason behind. They live in the mutation
	// worktree's build dir, never in the source tree.
	closeStdio, err := jobStdio(cmd, j)
	if err != nil {
		return 0, err
	}
	cmd.SysProcAttr = belowNormalAttrs()
	err = cmd.Start()
	closeStdio()
	if err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// jobStdio points the job's three streams at its own log files, creating the
// directory they live in. stdin is the null device: a detached job has no
// terminal to read from, and a producer that blocks on one would hang for
// ever instead of failing.
func jobStdio(cmd *exec.Cmd, j MutantsJob) (func(), error) {
	if j.Log == "" || j.ErrLog == "" {
		return func() {}, errors.New("job names no log files")
	}
	if err := os.MkdirAll(filepath.Dir(j.Log), 0o755); err != nil {
		return func() {}, err
	}
	out, err := os.Create(j.Log)
	if err != nil {
		return func() {}, err
	}
	errFile, err := os.Create(j.ErrLog)
	if err != nil {
		out.Close()
		return func() {}, err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		out.Close()
		errFile.Close()
		return func() {}, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, out, errFile
	return func() {
		null.Close()
		out.Close()
		errFile.Close()
	}, nil
}

func mutantsJobFilePath(j MutantsJob) string {
	dir := mutantsStateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "job."+projectKey(j.RepoRoot)+"."+short(j.TipTree)+".json")
}

func writeMutantsJobFile(path string, j MutantsJob) error {
	if path == "" {
		return errors.New("no state dir for the job file")
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func readMutantsJob(path string) (MutantsJob, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return MutantsJob{}, errors.New("no job file given")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return MutantsJob{}, fmt.Errorf("cannot read the job file %s: %w", path, err)
	}
	var j MutantsJob
	if err := json.Unmarshal(data, &j); err != nil {
		return MutantsJob{}, fmt.Errorf("the job file %s is not readable JSON: %w", path, err)
	}
	if j.Worktree == "" || j.Tip == "" {
		return MutantsJob{}, fmt.Errorf("the job file %s names no worktree or no tip, so there is nothing to measure", path)
	}
	return j, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func logf(f io.Writer, format string, args ...any) {
	if f == nil {
		return
	}
	fmt.Fprintf(f, format+"\n", args...)
}
