package mutation

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// The commit gate's own mutation stage, declared with `mutants-at-commit`.
// A commit adds or changes some lines; this stage mutates those lines, runs
// each mutant against the tests selected for its enclosing function under the
// gate's memory cap, and refuses the commit when a test suite does not notice
// one — the same finding CI's mutants-verdict would refuse the PR for, named
// the way fail-first names a RED.
//
// It never blocks on a slow box. A box with no memory to spare, a box-wide
// mutation run holding the lock for longer than the budget, or a budget spent
// before every mutant was judged leaves the mutants it did not reach NOT
// MEASURED: reported, counted in the gate log, and left to CI.

// mutantsAtCommitStage is the stage. It is inert in a repo that does not
// declare the key, and says nothing at all there.
func mutantsAtCommitStage(displayName, repoRoot string) GateResult {
	cfg, err := ReadMutantsConfig(repoRoot)
	if err != nil || !cfg.AtCommit {
		// The merge gate refuses a broken config loudly; this stage runs on
		// every commit of every repo on the box and stays out of it.
		return mutantsResult(false, "")
	}
	if !isGoModuleRepo(repoRoot) {
		return commitStandDown(displayName, repoRoot, "this repo is not a Go module, and the commit-time run measures Go", "not-go")
	}
	start := commitNowFn()
	added, err := stagedAddedLines(repoRoot)
	if err != nil {
		return commitUnmeasured(displayName, repoRoot, "diff", err.Error())
	}
	unstaged, err := unstagedFiles(repoRoot)
	if err != nil {
		return commitUnmeasured(displayName, repoRoot, "diff", err.Error())
	}
	return measureAddedLines(displayName, repoRoot, cfg, added, unstaged, start)
}

// measureAddedLines is the stage past the reading of git: mutate the added
// lines of the sources in added, run them, and judge. start is when the
// stage began, since the budget counts from there. The staged change and an
// edit's own diff both come through here.
func measureAddedLines(displayName, repoRoot string, cfg MutantsConfig, added map[string]map[int]bool,
	unstaged map[string]bool, start time.Time) GateResult {
	mutants, notes := commitMutantsOf(repoRoot, added, unstaged)
	for _, note := range notes {
		fmt.Fprintf(os.Stderr, "gate %s: mutants → %s\n", displayName, note)
	}
	if len(mutants) == 0 {
		return commitStandDown(displayName, repoRoot, "no mutable line is added by this commit", "nothing-to-measure")
	}
	budget := cfg.CommitBudget()
	if why := commitHeadroomFn(repoRoot, 0); why != "" {
		return commitUnmeasured(displayName, repoRoot, "headroom", why)
	}
	release, ok := acquireMutantsRunLockWithDeadline("mutants at commit for "+repoRoot, repoRoot, budget)
	if !ok {
		return commitUnmeasured(displayName, repoRoot, "lock",
			fmt.Sprintf("another mutation run held the box-wide lock for the whole %s budget", budget))
	}
	defer release()
	jobs, _ := mutantsJobsForThisBoxFn(mutantsGoJobGB)
	plans := commitPlans(repoRoot, mutants, added)
	canary := watchGitWorld(repoRoot, "commit-time run")
	runs := runCommitMutants(context.Background(), repoRoot, cfg, plans, mutants, jobs, budget-commitNowFn().Sub(start), os.Stderr)
	if changes := canary.verify(io.Discard); len(changes) > 0 {
		// The tests the run started reached the real git state: refuse the
		// commit rather than judge it on what such a run said.
		AppendGateLog(displayName, repoRoot, "mutants", "mutants-refused:git-world-changed", 0)
		return mutantsResult(true, gitWorldRefusal("commit-time run", repoRoot, changes))
	}
	return commitVerdict(displayName, repoRoot, cfg, runs, commitNowFn().Sub(start))
}

// commitStandDown passes the commit without measuring, saying why on stderr
// and counting the reason in the gate log.
func commitStandDown(displayName, repoRoot, reason, token string) GateResult {
	fmt.Fprintf(os.Stderr, "gate %s: mutants → skipped (%s)\n", displayName, reason)
	AppendGateLog(displayName, repoRoot, "mutants", "mutants-skipped:"+token, 0)
	return mutantsResult(false, "")
}

// commitUnmeasured passes the commit without measuring because the box
// could not: this commit carries no commit-time mutation evidence, which is
// an absence and not a pass, and CI's mutants-verdict decides it.
func commitUnmeasured(displayName, repoRoot, kind, why string) GateResult {
	fmt.Fprintf(os.Stderr, "gate %s: mutants → NOT MEASURED (%s) — this commit carries no commit-time mutation evidence; CI's mutants-verdict decides\n",
		displayName, why)
	AppendGateLog(displayName, repoRoot, "mutants", "mutants-unmeasured:commit-"+kind, 0)
	return mutantsResult(false, "")
}

// slowestMutant is the longest any one mutant of the run took.
func slowestMutant(runs []commitRun) time.Duration {
	var slowest time.Duration
	for _, r := range runs {
		slowest = max(slowest, r.Took)
	}
	return slowest
}

// commitVerdict judges what the run measured, logs it, and answers the
// stage's result: a refusal naming each survivor, or a pass.
func commitVerdict(displayName, repoRoot string, cfg MutantsConfig, runs []commitRun, elapsed time.Duration) GateResult {
	v, measured, unmeasured := commitReport(cfg, runs)
	logRoot := measureLogRoot(repoRoot)
	seen := map[string]bool{}
	for _, r := range runs {
		if r.NotMeasured != "" && !seen[r.GapKind] {
			seen[r.GapKind] = true
			AppendGateLog(displayName, logRoot, "mutants", "mutants-unmeasured:commit-"+r.GapKind, elapsed)
		}
	}
	tail := ""
	if unmeasured != "" {
		tail = fmt.Sprintf("\nmutants: NOT MEASURED (%s) — CI's mutants-verdict decides these", unmeasured)
	}
	if measured == 0 {
		fmt.Fprintf(os.Stderr, "gate %s: mutants → NOT MEASURED%s\n", displayName, strings.TrimPrefix(tail, "\nmutants: NOT MEASURED"))
		return mutantsResult(false, "")
	}
	AppendGateLog(displayName, logRoot, "mutants", measureLogVerdict(v), elapsed)
	timing := fmt.Sprintf("%s, slowest mutant %s", elapsed.Round(100*time.Millisecond), slowestMutant(runs).Round(100*time.Millisecond))
	if !v.Refused {
		fmt.Fprintf(os.Stderr, "gate %s: mutants → %d tested, %d caught, %d unviable, %d accepted (%s)%s\n",
			displayName, v.Tested, v.Caught, v.Unviable, v.Accepted, timing, tail)
		return mutantsResult(false, "")
	}
	msg := fmt.Sprintf("gate %s: mutants → REJECTED — a mutant of a line this commit adds survived its tests (%s)\n%s%s",
		displayName, timing, v.Message, tail)
	fmt.Fprintln(os.Stderr, msg)
	return mutantsResult(true, msg)
}
