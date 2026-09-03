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
	// MutantsJobsWhyEnv the reason a runner prints beside it.
	MutantsJobsEnv    = "APHROLLO_MUTANTS_JOBS"
	MutantsJobsWhyEnv = "APHROLLO_MUTANTS_JOBS_WHY"
)

// RunMutantsJob is the detached wrapper's body. It never blocks anything, so
// it always exits 0; what it has to say goes in the job's log and in
// gate.log.
// Its own stdout and stderr ARE the job's log files: the parent redirected
// them at spawn, so everything this function and the producer print is already
// going where a later merge can read it.
func RunMutantsJob(jobPath string) int {
	lowerOwnPriority()
	j, ok := readMutantsJob(jobPath)
	if !ok {
		return 0
	}
	log := os.Stdout

	if err := prepareMutantsWorktree(j); err != nil {
		logf(log, "aphrollo: could not prepare %s: %v", j.Worktree, err)
		appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-worktree-failed", 0)
		return 0
	}
	plan, carried := scopeMutantsRun(j)
	// Whatever an interrupted attempt on this same tree already reached: those
	// mutants are excluded from this run and their verdicts kept.
	judged := loadMutantsPartials(j.TipTree)
	if len(plan) == 0 && len(carried) == 0 {
		logf(log, "aphrollo: nothing mutable in this lane's diff")
		appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-nothing-to-do", 0)
		return 0
	}
	if err := writeLaneDiff(j, plan); err != nil {
		logf(log, "aphrollo: could not write the lane diff: %v", err)
		return 0
	}
	logf(log, "aphrollo: %d file(s) to measure, %d carried from the previous run, %d already judged by an interrupted attempt",
		len(plan), len(carried), len(judged))

	start := time.Now()
	code := runMutantsProducer(j, judged)
	// Read BEFORE judging the exit code: a run that died at mutant 101 of 131
	// still wrote 100 verdicts, and this is the read that stops them being
	// thrown away (issue #103).
	saveMutantsPartials(j.TipTree, readMutantsOut(j.Worktree))
	appendGateLog("mutants", logToken(j.Repo), "mutants", mutantsVerdict(code)+":"+short(j.TipTree), time.Since(start))
	if code == 0 {
		clearMutantsDeath(j.TipTree)
		adoptCarriedOutcomes(j, append(append([]MutantOutcome{}, carried...), judged...))
		// Every finished run feeds the shared cache, which is what makes the
		// NEXT lane over these blobs cheap.
		if r, ok := readReceiptFile(MutationReceiptPathFor(j.TipTree)); ok {
			MergeMutantStore(j.Repo, r.Outcomes)
		}
		return 0
	}
	recordMutantsDeath(j, code, stderrTail(j.ErrLog, 3))
	return 0
}

// mutantNames renders the mutants an interrupted attempt already judged, in
// the spelling the tool names them by, for the restart's exclusions.
func mutantNames(judged []MutantOutcome) []string {
	out := make([]string, 0, len(judged))
	for _, m := range judged {
		out = append(out, fmt.Sprintf("%s:%d: %s", m.File, m.Line, m.Mutation))
	}
	return out
}

// storedWants is every mutant the store knows for this repo, as a list to
// plan over: the plan then decides which of them still describe the tip.
func storedWants(cached map[mutantKey]MutantOutcome) []MutantOutcome {
	out := make([]MutantOutcome, 0, len(cached))
	for _, m := range cached {
		out = append(out, m)
	}
	sortOutcomes(out)
	return out
}

func mutantsVerdict(code int) string {
	if code == 0 {
		return "mutants-finished"
	}
	return "mutants-failed"
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
		if out, err := git(j.RepoRoot, "worktree", "add", "--detach", j.Worktree, j.Tip); err != nil {
			return errors.New(strings.TrimSpace(out))
		}
		return nil
	}
	if out, err := git(j.Worktree, "reset", "-q", "--hard", j.Tip); err != nil {
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

// scopeMutantsRun decides what this run must measure and what it inherits:
// the lane's changed files minus the ones whose blob and whose package's test
// set are both unchanged since the newest receipt on the same base.
func scopeMutantsRun(j MutantsJob) (files []string, carried []MutantOutcome) {
	lane, ok := changedPaths(j.RepoRoot, j.BaseSHA, j.Tip)
	if !ok {
		return nil, nil
	}
	now := treeStateAt(j.RepoRoot, j.Tip)
	// The repo-wide store, not this lane's own last receipt: a verdict is a
	// fact about a blob and a test set, so a lane that touches a file another
	// lane already measured at the same blob measures nothing for it.
	cached := LoadMutantStore(j.Repo)
	files = PlanDiffFiles(lane, now, cached)
	carried = PlanMutants(storedWants(cached), now, cached).Carry
	return files, carried
}

// writeLaneDiff writes the reduced diff the run is scoped to: the lane's own
// changes, restricted to the files the plan says still have to be measured.
func writeLaneDiff(j MutantsJob, files []string) error {
	if j.Diff == "" {
		return errors.New("job names no diff file")
	}
	args := append([]string{"diff", j.BaseSHA, j.Tip, "--"}, files...)
	out, err := git(j.RepoRoot, args...)
	if err != nil {
		return errors.New(strings.TrimSpace(out))
	}
	return os.WriteFile(j.Diff, []byte(out), 0o600)
}

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
		return []string{"bash", filepath.ToSlash(script), j.BaseSHA}, true
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
		BuildLockHeldEnv: true,
	}
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); !ok || !drop[k] {
			out = append(out, kv)
		}
	}
	jobs, why := mutantsJobsForThisBox()
	ws := cargoWorkspaceRoot(j.Worktree)
	if ws == "" {
		ws = j.Worktree
	}
	out = append(out, mutantsTempEnv(j)...)
	return append(out,
		"CARGO_TARGET_DIR="+j.TargetDir,
		MutationGateEnv+"=1",
		QueueEnv+"="+QueueBypass,
		MutantsArgsEnv+"="+strings.Join(MutantsArgv(j.Diff,
			TipSuiteGreen(j.RepoRoot, j.Started.Add(-mutantsGreenWindow)), mutantNames(judged)), " "),
		MutantsDiffEnv+"="+j.Diff,
		MutantsBaseEnv+"="+j.BaseSHA,
		// The switches the repo says its mutation run must set: without them
		// every mutant behind an env-gated suite is missed by construction.
		MutantsEnvEnv+"="+strings.Join(cargoMutantsEnv(ws), " "),
		MutantsJobsEnv+"="+strconv.Itoa(jobs),
		MutantsJobsWhyEnv+"="+why,
		"CI=1", "NO_COLOR=1")
}

// mutantsGreenWindow is how far back a green commit-gate run still counts as
// this tip's. The gate runs seconds before the commit it lets through; half
// an hour is slack for a slow suite, not a second lane's answer.
const mutantsGreenWindow = 30 * time.Minute

// adoptCarriedOutcomes folds the outcomes this run inherited into the receipt
// the producer wrote, so the receipt describes the WHOLE lane diff rather than
// only the part that was re-measured — and so the next run has something to
// carry in turn.
func adoptCarriedOutcomes(j MutantsJob, carried []MutantOutcome) {
	path := MutationReceiptPathFor(j.TipTree)
	if path == "" || len(carried) == 0 {
		return
	}
	r, ok := readReceiptFile(path)
	if !ok {
		return
	}
	have := map[mutantKey]bool{}
	for _, m := range r.Outcomes {
		have[m.key()] = true
	}
	for _, m := range carried {
		if !have[m.key()] {
			r.Outcomes = append(r.Outcomes, m)
			r.MutantsTotal++
			if m.Status == "caught" {
				r.Caught++
			}
		}
	}
	writeReceiptFile(path, r)
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

func readMutantsJob(path string) (MutantsJob, bool) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return MutantsJob{}, false
	}
	var j MutantsJob
	if err := json.Unmarshal(data, &j); err != nil {
		return MutantsJob{}, false
	}
	return j, j.Worktree != "" && j.Tip != ""
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
