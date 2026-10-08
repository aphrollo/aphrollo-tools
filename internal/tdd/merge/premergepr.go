package merge

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// A lane can land two ways, and only one of them fires a git hook. A local
// `git merge --no-ff lane/x` on the primary checkout fires pre-merge-commit,
// which runs Mechanical — the suites, and with mutants-at-merge declared the
// mutation measurement that refuses an unaccepted survivor by name. A lane
// landed through `workspace merge` is merged by GitHub: no local merge
// commit is ever made, pre-merge-commit never fires (nor would it for a
// fast-forward), and the tree is judged only by CI's per-job checks. Same
// repo, same declared policy, two standards.
//
// GatePRMerge closes that by building the merge locally BEFORE asking GitHub
// to make it: a throwaway checkout of trunk, the lane merged into it with
// --no-commit — which is exactly the state pre-merge-commit fires in, MERGE_HEAD
// and a merged index included — and then the SAME Mechanical call the hook
// makes. Nothing here re-implements a stage; the whole point is that the two
// paths run one gate.
//
// What it cannot do is gate the merge GitHub performs on its own account: a
// PR merged from the web UI, by another operator, or by an auto-merge queue
// never passes through this binary at all. This gate covers the `workspace
// merge` verb, which is the path this box takes.

// GatePRMerge judges the tree a PR merge is about to create, in the gate's own
// checkout, and returns the refusal a merge must not survive. It fires when
// there is anything on the merged tree to judge: the repo declares ratchet
// laws, or it declares mutants-at-merge, or both. Each stage inside keeps its
// own opt-in — declaring laws alone pulls in the merged-tree ratchet
// judgment (and, riding along in the same Mechanical call, its suites), never
// the mutation measurement, which still answers only to mutants-at-merge. A
// repo with neither pays nothing: no fetch, no checkout, the verb behaves
// exactly as it did before this existed. (harryberg1n/borld#455, #456: the
// mutation flag used to gate this whole judgment, so a repo with laws but no
// mutants-at-merge got none of it — the gap two lanes, each green alone, used
// to land a law regression only their merge crossed.)
//
// Every uncertainty refuses. A trunk that cannot be resolved, a merge that
// cannot be built, a checkout that cannot be made: none of those measured
// anything, and a merge that lands on an unrun gate is the hole this closes,
// not a case to be waved through.
func GatePRMerge(laneWorktree, head string, run SuiteRunner, log io.Writer) error {
	if log == nil {
		log = io.Discard
	}
	cfg, err := ReadMutantsConfig(laneWorktree)
	if err != nil {
		return prGateRefusal(laneWorktree, "config", "%v", err)
	}
	if !cfg.AtMerge && !ratchet.HasLaws(laneWorktree) {
		return nil
	}
	return judgeMergedTree(laneWorktree, head, run, log, nil)
}

// judgeMergedTree is the body both the merge gate and local CI run: build the
// merge in a throwaway checkout and hand it to Mechanical. tips, when the
// caller already resolved them, are not resolved again.
func judgeMergedTree(laneWorktree, head string, run SuiteRunner, log io.Writer, tips *prGateTips) error {
	return judgeMergedTreeWith(laneWorktree, head, run, log, tips, func(wt string) GateResult { return Mechanical(wt, run) })
}

// judgeMergedTreeWith is judgeMergedTree with the judgment of the built
// checkout left to the caller: the whole mechanical stage, or only the part a
// reused CI verdict leaves owing.
func judgeMergedTreeWith(laneWorktree, head string, run SuiteRunner, log io.Writer, tips *prGateTips, judge func(wt string) GateResult) error {
	if tips == nil {
		resolved, err := prGateTipsOf(laneWorktree, head, log)
		if err != nil {
			return err
		}
		tips = &resolved
	}
	if tips.landed {
		return nil // trunk already contains this lane: nothing lands, nothing to judge
	}
	co, err := prGateMergedCheckout(laneWorktree, *tips)
	if err != nil {
		return err
	}
	wt := co.Path
	// cleanupOnce guards the ONE real removal against running twice: the
	// normal return path and a signal caught mid-Mechanical both call
	// safeCleanup, and this stops a `git worktree remove` from racing its
	// own second call.
	// links are the node_modules links provisioning makes into wt; they are
	// removed as links BEFORE the checkout is deleted, so no removal can
	// reach through one into the lane's own node_modules.
	var links depinstall.Links
	var cleanupOnce sync.Once
	// A normal return hands the warm checkout back (the next merge resets
	// it); a signal mid-run deletes it, since what it holds is half a run.
	finish := func(done func()) {
		cleanupOnce.Do(func() {
			links.Remove()
			done()
		})
	}
	safeCleanup := func() { finish(co.Remove) }
	defer finish(co.Release)
	// Armed for exactly the window the checkout exists: Ctrl-C, a plain
	// `kill`, or the SIGHUP a killed background shell sends its children all
	// terminate a Go process immediately by default, running no deferred
	// cleanup at all — which is how a merge run in a background shell that
	// was later killed left its throwaway checkout registered with nobody
	// left to remove it. stopPRGateSignals is deferred FIRST (so it runs
	// LAST-declared-first-out, i.e. before safeCleanup above), disarming the
	// handler before the deferred cleanup above runs so the two can never
	// race each other on the way out.
	stopSignals := watchPRGateSignals(safeCleanup, log)
	defer stopSignals()
	if err := prGateProvisionNode(laneWorktree, wt, run, &links, log); err != nil {
		return err
	}
	fmt.Fprintf(log, "gate %s: judging %s merged into %s (in %s)\n", premergeDisplayName, tips.lane, tips.trunkRef, wt)
	if res := judge(wt); res.Blocked {
		return errors.New(res.Message)
	}
	return nil
}

// prGateTips is what the merge is between: the lane tip being landed and the
// trunk tip it lands on.
type prGateTips struct {
	lane, trunk, trunkRef string
	landed                bool
}

// prGateFetch refreshes the remote trunk so the merge this gate builds is the
// merge GitHub is about to make, not one against a trunk this box last saw
// hours ago. A seam, and a repo with no origin skips it: the LOCAL merge path
// must keep working with no network at all, and a test must never reach one.
var prGateFetch = func(dir string) error {
	if originURL(dir) == "" {
		return nil
	}
	_, err := git(dir, "fetch", "--quiet", "origin")
	return err
}

// prGateTipsOf resolves both ends of the merge. head is the commit being landed:
// the merge verb passes the PR head GitHub reported, so the tree judged is the
// tree that merges whatever the lane worktree has checked out; "" reads the
// checkout's own HEAD, which is what a run with no PR (ci run) judges. A head
// this checkout does not hold is refused, never read as HEAD. A failed fetch is reported and
// not refused — the gate then judges the merge against the trunk this box
// already has, which is a weaker base but still a real measurement of the
// lane's own change, and refusing here would ground the verb on a flaky
// network the merge itself already got through.
func prGateTipsOf(laneWorktree, head string, log io.Writer) (prGateTips, error) {
	trunk := TrunkBranch(laneWorktree)
	if trunk == "" {
		return prGateTips{}, prGateRefusal(laneWorktree, "no-trunk",
			"this repo's trunk branch could not be resolved, so the merge could not be built and judged")
	}
	if err := prGateFetch(laneWorktree); err != nil {
		fmt.Fprintf(log, "gate %s: could not refresh %s (%v) — judging against the trunk this checkout already has\n",
			premergeDisplayName, trunk, err)
	}
	tips := prGateTips{trunkRef: trunk}
	tips.trunk = refSHA(laneWorktree, trunk)
	if head == "" {
		head = "HEAD"
	}
	tips.lane = strings.TrimSpace(gitOut(laneWorktree, "rev-parse", "--verify", "--quiet", head+"^{commit}"))
	if head != "HEAD" && tips.lane == "" {
		return prGateTips{}, prGateRefusal(laneWorktree, "no-head",
			"the PR head %s is not a commit this checkout holds, so the merge could not be built and judged", shortCommit(head))
	}
	if tips.trunk == "" || tips.lane == "" {
		return prGateTips{}, prGateRefusal(laneWorktree, "no-tips",
			"neither %s nor HEAD resolved to a commit, so the merge could not be built and judged", trunk)
	}
	tips.landed = strings.TrimSpace(gitOut(laneWorktree, "merge-base", tips.lane, tips.trunk)) == tips.lane
	return tips, nil
}

// prGateMergedCheckout builds the merged tree the PR is about to create and
// hands back the checkout it lives in. `--no-commit` leaves MERGE_HEAD and the
// merged index in place, which is the state Mechanical's own stages read: the
// mutation base becomes the merge base with the incoming tip, exactly as it
// does under the pre-merge-commit hook.
func prGateMergedCheckout(laneWorktree string, tips prGateTips) (prGateCheckout, error) {
	co, err := prGateCheckoutAt(laneWorktree, prGateWarmName, tips.trunk)
	if err != nil {
		return prGateCheckout{}, prGateRefusal(laneWorktree, "no-checkout",
			"a checkout of %s to build the merge in could not be made (%v), so the merge was never judged",
			tips.trunkRef, err)
	}
	if out, err := git(co.Path, "merge", "--no-commit", "--no-ff", tips.lane); err != nil {
		_, _ = git(co.Path, "merge", "--abort")
		co.Remove()
		return prGateCheckout{}, prGateRefusal(laneWorktree, "no-merge-tree",
			"this lane does not merge cleanly into %s here, so the merged tree could not be judged"+
				" — update the lane (git fetch && git merge %s) and try again\n%s",
			tips.trunkRef, tips.trunkRef, strings.TrimSpace(out))
	}
	return co, nil
}

// prGateRemoveCheckout deletes the throwaway checkout wt and the measurement
// area beside it. A seam, so a test can stand in a remover that follows links
// and prove the node_modules links are gone before this ever runs.
//
// Every link in wt is unlinked first, not only the ones provisioning
// recorded: git deletes an ignored node_modules with the tree, and through a
// junction that is the lane's own install (#947). A link that cannot be
// unlinked leaves the checkout in place for a later sweep.
var prGateRemoveCheckout = func(laneWorktree, wt string) {
	if depinstall.RemoveLinks(wt) == nil {
		_, _ = git(laneWorktree, "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(wt)
	}
	// measureTempDir puts the run's measurement area BESIDE wt, not
	// inside it, precisely so a tree copy never shares a lock with the
	// checkout it is copied from — which means removing wt alone leaves
	// that area behind. Every merge through this gate builds and
	// abandons one; this is what stops it from leaking.
	_ = depinstall.RemoveTree(measureTempDir(wt))
}

// PRGateHolderFile is the record prGateWriteHolder leaves in its own
// checkout: this process's pid and when it started, so a sweep run after a
// crash (aphrollo gate gc, workspace prune) can tell a dead gate's throwaway
// checkout from one still being built, without guessing. Exported so the gc
// and workspace prune sweeps read the exact name this side writes.
const PRGateHolderFile = ".aphrollo-prmerge-holder"

// prGateWriteHolder records this process as wt's holder. Best-effort: a
// write failure never blocks the merge judgment itself, it only costs a
// later sweep its ability to tell this checkout apart from a live one — the
// same trade-off writeBuildLockOwnerAt (internal/tdd/lock) already makes for
// the box-wide build lock.
func prGateWriteHolder(wt string) {
	data := fmt.Sprintf("pid=%d\nstarted=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	_ = os.WriteFile(filepath.Join(wt, PRGateHolderFile), []byte(data), 0o644)
}

// prGateCheckoutParent is where the throwaway merged checkout is built: beside
// the repo's lanes, <parent of the primary>/.worktrees/<repo>, so it and the
// measurement area measureTempDir puts next to it share the repo's own disk.
// The OS temp dir is a RAM-backed tmpfs on many Linux boxes and C: on Windows
// whatever drive the repo is on; a measurement there is refused for space, or
// fills the wrong drive. "" (the OS temp dir) only when no primary resolves.
func prGateCheckoutParent(laneWorktree string) string {
	primary := primaryCheckoutRoot(laneWorktree)
	if primary == "" {
		return ""
	}
	dir := filepath.Join(filepath.Dir(primary), ".worktrees", filepath.Base(primary))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// prGateRefusal is every refusal this side makes: one message shape carrying
// the gate's own name, and one gate.log line under the merge gate's token and
// the REAL repo root — the throwaway checkout is not a repo anyone reads
// statistics about.
func prGateRefusal(repoRoot, token, format string, args ...any) error {
	msg := fmt.Sprintf("gate %s: %s", premergeDisplayName, fmt.Sprintf(format, args...))
	AppendGateLog(premergeDisplayName, repoRoot, "pr-merge", "premerge-refused:"+token, 0)
	return errors.New(msg)
}
