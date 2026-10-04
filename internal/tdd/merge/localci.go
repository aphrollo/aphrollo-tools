package merge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ghworkflow"
)

// Local CI is the repo's own GitHub workflow run on this box, so a local green
// means what a GitHub green means. It runs in a throwaway worktree of the merge
// result (the lane merged into trunk, made as a commit with git plumbing so no
// hook fires), through internal/ghworkflow: every pull_request workflow's jobs
// in needs order, each run: step under bash. uses: steps are not executed and
// are printed by name. Mutation is not part of it: hosted CI measures it, and
// the pre-merge gate measures it where a repo declares mutants-at-merge.
//
// The verdict is a gate.log line keyed by the merge result's tree hash, so
// `gate stats` and `gate output` show it like any gate result, and the same
// tree is never judged twice: a stored green stands in for the run.

// CI modes a repo or a merge can choose.
const (
	CIAuto   = "auto"
	CILocal  = "local"
	CIGithub = "github"
)

// ciStage is the gate.log stage local CI records under.
const ciStage = "ci"

// ciModeKey is the aphrollo.toml key naming the repo's CI choice.
const ciModeKey = "ci"

// ReadCIMode is the repo's declared CI choice, auto when it declares none. An
// unknown value is an error naming the allowed ones: a typo that quietly
// fell back to auto would send a merge to GitHub the repo meant to avoid.
func ReadCIMode(root string) (string, error) {
	v, set := tomlStringIn(root+"/aphrollo.toml", "[aphrollo]", ciModeKey)
	if !set || strings.TrimSpace(v) == "" {
		return CIAuto, nil
	}
	return NormalizeCIMode(v)
}

// NormalizeCIMode validates a CI mode, from the config or from --ci.
func NormalizeCIMode(v string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(v)); m {
	case CIAuto, CILocal, CIGithub:
		return m, nil
	}
	return "", fmt.Errorf("ci = %q is not a CI mode (want auto | local | github)", v)
}

// ciScratchGrace is how long a signalled process waits for the cancelled run to
// remove its own scratch before it removes it.
const ciScratchGrace = 5 * time.Second

// LocalCIVerdict is what a local CI run answered: the tree it judged and
// whether a stored green stood in for a run.
type LocalCIVerdict struct {
	Tree   string
	Reused bool
	Landed bool // trunk already holds the lane: nothing to judge
	Red    bool // the workflows ran and a job failed (an error alone may be a setup refusal)
}

// LocalCI judges the merge of laneWorktree's HEAD into trunk by running the
// repo's pull_request workflows. A stored green for the same merge-result tree
// is reused; otherwise the verdict, green or red, is recorded.
func LocalCI(laneWorktree string, log io.Writer) (LocalCIVerdict, error) {
	return LocalCIWith(laneWorktree, log, CIRunOptions{})
}

// LocalCIWith is LocalCI with the run's knobs: what the caller leaves zero is
// taken from the repo's aphrollo.toml (ci-jobs, ci-timeout), and a malformed
// value there is refused before anything is judged.
func LocalCIWith(laneWorktree string, log io.Writer, run CIRunOptions) (LocalCIVerdict, error) {
	if log == nil {
		log = io.Discard
	}
	run, err := resolveCIRunOptions(laneWorktree, run)
	if err != nil {
		return LocalCIVerdict{}, err
	}
	tips, err := prGateTipsOf(laneWorktree, run.Head, log)
	if err != nil {
		return LocalCIVerdict{}, err
	}
	if tips.landed {
		return LocalCIVerdict{Landed: true}, nil
	}
	tree, commit, err := buildMergeResult(laneWorktree, tips)
	if err != nil {
		return LocalCIVerdict{}, err
	}
	if storedGreen(tree) {
		fmt.Fprintf(log, "ci local: merge result %s already judged green — reusing that verdict\n", tree)
		return LocalCIVerdict{Tree: tree, Reused: true}, nil
	}
	wt, cleanup, err := ciCheckout(laneWorktree, commit)
	if err != nil {
		return LocalCIVerdict{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	// A signal exits the process from its handler, so Run never returns to remove
	// its own scratch: stop the run's steps, then remove what it left.
	safeCleanup := func() {
		once.Do(func() {
			cancel()
			ghworkflow.RemoveLiveScratch(ciScratchGrace)
			cleanup()
		})
	}
	defer safeCleanup()
	defer watchPRGateSignals(safeCleanup, log)()
	fmt.Fprintf(log, "ci local: judging %s merged into %s (tree %s, in %s)\n", tips.lane, tips.trunkRef, tree, wt)
	start := time.Now()
	sum, err := runWorkflows(ctx, laneWorktree, wt, tips, commit, log, run)
	if err != nil {
		return LocalCIVerdict{Tree: tree}, err
	}
	refused := sum.Refused()
	verdict := "green"
	switch {
	case sum.Failed():
		verdict = "red"
	case len(refused) > 0:
		verdict = "inconclusive"
	}
	AppendGateLog(ciStage, laneWorktree, "local-ci:"+tree, verdict, time.Since(start))
	fmt.Fprintf(log, "ci local: %s — %d job(s) ran, %d skipped\n", verdict,
		sum.Count(ghworkflow.ResultSuccess)+sum.Count(ghworkflow.ResultFailure), sum.Count(ghworkflow.ResultSkipped))
	if sum.Failed() {
		return LocalCIVerdict{Tree: tree, Red: true}, fmt.Errorf("local CI is red: %s", failedJobs(sum))
	}
	if len(refused) > 0 {
		return LocalCIVerdict{Tree: tree}, fmt.Errorf("local CI is inconclusive: a step it would not run was refused, so this tree is not proven (%s)", strings.Join(refused, "; "))
	}
	return LocalCIVerdict{Tree: tree}, nil
}

func failedJobs(sum *ghworkflow.Summary) string {
	var parts []string
	for _, j := range sum.Jobs {
		if j.Result == ghworkflow.ResultFailure {
			parts = append(parts, fmt.Sprintf("%s: %s (%s)", j.Workflow, j.ID, j.Detail))
		}
	}
	return strings.Join(parts, "; ")
}

// runWorkflows loads the merge result's pull_request workflows and runs them.
// It records nothing: a missing or unreadable workflow is a refusal, not a
// verdict, since no job judged the tree.
func runWorkflows(ctx context.Context, lane, wt string, tips prGateTips, commit string, log io.Writer, run CIRunOptions) (*ghworkflow.Summary, error) {
	flows, skipped, err := ghworkflow.LoadDir(wt)
	if err != nil {
		return nil, fmt.Errorf("local CI could not read this repo's workflows: %w", err)
	}
	for _, s := range skipped {
		fmt.Fprintf(log, "ci local: [skip] workflow %s\n", s)
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("local CI has nothing to run: no workflow under .github/workflows runs on pull_request")
	}
	event := map[string]any{
		"sha": commit, "base_sha": tips.trunk, "head_sha": tips.lane,
		"head_ref":   laneBranchOf(lane),
		"base_ref":   strings.TrimPrefix(tips.trunkRef, "origin/"),
		"repository": repoSlug(originURL(lane)),
	}
	return ghworkflow.Run(ctx, flows, ghworkflow.Options{Dir: wt, Out: log, Event: event, Jobs: run.Jobs, StepTimeout: run.StepTimeout})
}

// repoSlug is owner/repo from a GitHub remote URL, "" for anything else.
func repoSlug(url string) string {
	_, after, ok := strings.Cut(url, "github.com")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(strings.Trim(after, ":/"), ".git")
}

// buildMergeResult makes the merge of the lane into trunk without touching any
// worktree: its tree, and a commit of it with both tips as parents, so the
// throwaway checkout has a real merge commit as HEAD.
func buildMergeResult(lane string, tips prGateTips) (tree, commit string, err error) {
	out, err := git(lane, "merge-tree", "--write-tree", tips.trunk, tips.lane)
	if err != nil {
		return "", "", prGateRefusal(lane, "no-merge-tree",
			"this lane does not merge cleanly into %s here, so the merged tree could not be judged"+
				" — update the lane (git fetch && git merge %s) and try again\n%s",
			tips.trunkRef, tips.trunkRef, strings.TrimSpace(out))
	}
	tree = strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	commit, err = git(lane, "-c", "user.name=aphrollo", "-c", "user.email=aphrollo@localhost",
		"commit-tree", tree, "-p", tips.trunk, "-p", tips.lane, "-m", "aphrollo local CI: merge result")
	if err != nil {
		return "", "", prGateRefusal(lane, "no-merge-commit", "the merge result could not be committed (%v)\n%s", err, strings.TrimSpace(commit))
	}
	return tree, strings.TrimSpace(commit), nil
}

// ciCheckout is the throwaway worktree of the merge commit, built beside the
// repo's lanes like the merge gate's own and swept by the same holder file.
func ciCheckout(lane, commit string) (string, func(), error) {
	wt, err := os.MkdirTemp(prGateCheckoutParent(lane), "gate-prmerge-")
	if err != nil {
		return "", nil, prGateRefusal(lane, "no-checkout", "a checkout to run CI in could not be created (%v), so the merge was never judged", err)
	}
	if out, err := git(lane, "worktree", "add", "--detach", wt, commit); err != nil {
		_ = os.RemoveAll(wt)
		return "", nil, prGateRefusal(lane, "no-checkout", "a checkout of the merge result could not be made (%v), so the merge was never judged\n%s", err, strings.TrimSpace(out))
	}
	prGateWriteHolder(wt)
	return wt, func() { prGateRemoveCheckout(lane, wt) }, nil
}

// storedGreen reports whether gate.log holds a green local CI verdict for tree.
func storedGreen(tree string) bool {
	path := GateLogPath()
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	want := "local-ci:" + tree
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if e, ok := parseGateLine(sc.Text()); ok && e.Stage == ciStage && e.Cmd == want && e.Verdict == "green" {
			return true
		}
	}
	return false
}
