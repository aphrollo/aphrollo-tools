package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
)

// RunMutantsJob is the detached wrapper's body. It never blocks anything, so
// it always exits 0; what it has to say goes in the job's log and in
// gate.log.
func RunMutantsJob(jobPath string) int {
	lowerOwnPriority()
	j, ok := readMutantsJob(jobPath)
	if !ok {
		return 0
	}
	log, err := os.Create(j.Log)
	if err != nil {
		return 0
	}
	defer log.Close()

	if err := prepareMutantsWorktree(j); err != nil {
		logf(log, "aphrollo: could not prepare %s: %v", j.Worktree, err)
		appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-worktree-failed", 0)
		return 0
	}
	plan, carried := scopeMutantsRun(j)
	if len(plan) == 0 && len(carried) == 0 {
		logf(log, "aphrollo: nothing mutable in this lane's diff")
		appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-nothing-to-do", 0)
		return 0
	}
	if err := writeLaneDiff(j, plan); err != nil {
		logf(log, "aphrollo: could not write the lane diff: %v", err)
		return 0
	}
	logf(log, "aphrollo: %d file(s) to measure, %d outcome(s) carried from the previous run", len(plan), len(carried))

	start := time.Now()
	code := runMutantsProducer(j, log)
	appendGateLog("mutants", logToken(j.Repo), "mutants", mutantsVerdict(code)+":"+short(j.TipTree), time.Since(start))
	if code == 0 {
		adoptCarriedOutcomes(j, carried)
	}
	return 0
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
	prev := newestReceiptOnBase(j.Repo, j.BaseSHA, j.TipTree)
	files = PlanDiffFiles(lane, now, prev)
	if prev != nil {
		carried = PlanMutants(prev.Outcomes, now, prev).Carry
	}
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
func runMutantsProducer(j MutantsJob, log *os.File) int {
	argv, ok := mutantsProducerArgv(j)
	if !ok {
		logf(log, "aphrollo: no mutation runner in %s (expected tools/mutation_gate.sh)", j.Worktree)
		return 1
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = j.Worktree
	cmd.Env = mutantsChildEnv(j)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		logf(log, "aphrollo: %v", err)
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
func mutantsChildEnv(j MutantsJob) []string {
	out := make([]string, 0, len(os.Environ())+8)
	drop := map[string]bool{
		"CARGO_TARGET_DIR": true, MutationGateEnv: true, QueueEnv: true,
		MutantsArgsEnv: true, MutantsDiffEnv: true, MutantsBaseEnv: true,
		BuildLockHeldEnv: true,
	}
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); !ok || !drop[k] {
			out = append(out, kv)
		}
	}
	return append(out,
		"CARGO_TARGET_DIR="+j.TargetDir,
		MutationGateEnv+"=1",
		QueueEnv+"="+QueueBypass,
		MutantsArgsEnv+"="+strings.Join(MutantsArgv(j.Diff, TipSuiteGreen(j.RepoRoot, j.Started.Add(-mutantsGreenWindow))), " "),
		MutantsDiffEnv+"="+j.Diff,
		MutantsBaseEnv+"="+j.BaseSHA,
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
	closeStdio := silentStdio(cmd)
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

func logf(f *os.File, format string, args ...any) {
	if f == nil {
		return
	}
	fmt.Fprintf(f, format+"\n", args...)
}
