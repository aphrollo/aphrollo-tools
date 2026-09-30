package mutation

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// mutants-at-merge = "ci": the measurement is the PR pipeline's
// `mutants-verdict` check, run on GitHub-hosted runners, and the local box
// measures nothing. What the merge gate still owes is the check's verdict: it
// refuses a merge unless that check concluded success on the PR's head commit.

// mutantsCICheck is the required check that carries the measurement. Branch
// protection names it, so the pipeline's aggregate job keeps this exact name.
const mutantsCICheck = "mutants-verdict"

// MutantsCISkipLine is the one line the local stages print instead of
// measuring.
const MutantsCISkipLine = "mutants: measured in CI (" + mutantsCICheck + ")"

// ciCheckFn reads the state of the mutants-verdict check on a commit, as
// "<status>/<conclusion>", or "" when the commit has no such check. A seam so
// a test states what GitHub answered without a network or a token.
var ciCheckFn = fetchCICheck

// setCICheckForTest replaces ciCheckFn for one test and answers the restore.
func setCICheckForTest(fn func(root, sha string) (string, error)) (restore func()) {
	prev := ciCheckFn
	ciCheckFn = fn
	return func() { ciCheckFn = prev }
}

// ciCheckTimeout bounds the one gh call, for the reason runnerFetchTimeout
// bounds the others: a merge somebody is waiting on must not hang on it.
const ciCheckTimeout = 90 * time.Second

// fetchCICheck asks GitHub for the newest check run named mutants-verdict on
// sha.
func fetchCICheck(root, sha string) (string, error) {
	if !ghAvailable() {
		return "", fmt.Errorf("the GitHub CLI is not installed here, so the check could not be read")
	}
	if !hasGitHubRemote(root) {
		return "", fmt.Errorf("this checkout has no GitHub remote, so there is no check to read")
	}
	out, err := runGhTimeout(root, ciCheckTimeout, "api",
		"repos/{owner}/{repo}/commits/"+sha+"/check-runs?per_page=100&filter=latest",
		"--jq", `[.check_runs[] | select(.name=="`+mutantsCICheck+`")] | first | if . == null then "" else .status + "/" + (.conclusion // "") end`)
	if err != nil {
		return "", fmt.Errorf("the check runs of %s could not be listed: %w", sha, err)
	}
	return strings.TrimSpace(out), nil
}

// judgeCICheck turns the check's "<status>/<conclusion>" into whether the
// merge may go on, and why not.
func judgeCICheck(state string) (ok bool, why string) {
	status, conclusion, _ := strings.Cut(state, "/")
	switch {
	case state == "":
		return false, "no " + mutantsCICheck + " check exists on that commit yet"
	case status != "completed":
		return false, mutantsCICheck + " is " + strings.ReplaceAll(status, "_", " ") + ", not finished"
	case conclusion == "success":
		return true, ""
	}
	return false, mutantsCICheck + " concluded " + conclusion
}

// mutantsCIStage is the merge gate's whole mutation stage under "ci": it says
// the measurement is CI's, and refuses unless CI's check passed on the tip
// being merged.
func mutantsCIStage(displayName, repoRoot string) GateResult {
	fmt.Fprintln(os.Stderr, MutantsCISkipLine)
	refuse := func(token, format string, args ...any) GateResult {
		msg := fmt.Sprintf("gate %s: mutants → REJECTED\n  %s", displayName, fmt.Sprintf(format, args...))
		fmt.Fprintln(os.Stderr, msg)
		AppendGateLog(displayName, repoRoot, "mutants", "mutants-refused:"+token, 0)
		return mutantsResult(true, msg)
	}
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		return refuse("no-lane-tip", "no lane tip to read the %s check of (neither .git/MERGE_HEAD nor %s names a merged branch)",
			mutantsCICheck, reflogActionEnv)
	}
	sha := strings.TrimSpace(gitOut(repoRoot, "rev-parse", tip.Rev+"^{commit}"))
	if sha == "" {
		return refuse("no-lane-tip", "the merged tip %s (named by %s) does not resolve to a commit", tip.Rev, tip.From)
	}
	state, err := ciCheckFn(repoRoot, sha)
	if err != nil {
		return refuse("ci-unreadable", "%s could not be read on %s (%v), so the merge is not shown to be measured", mutantsCICheck, sha, err)
	}
	if ok, why := judgeCICheck(state); !ok {
		return refuse("ci-not-passed", "%s did not pass on the PR head %s: %s", mutantsCICheck, sha, why)
	}
	AppendGateLog(displayName, repoRoot, "mutants", "mutants-ci:passed", 0)
	return mutantsResult(false, "")
}
