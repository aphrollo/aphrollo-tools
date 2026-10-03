package merge

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// CI judges every PR on the merge ref GitHub builds for it, on each OS the
// repo declares. When the tree the merge gate is about to test is that same
// tree, and those checks passed on this very head, running the suites again on
// the box tells nobody anything new: it only costs the minutes that made the
// local run outlast its cap. GatePRMergeReusingCI takes CI's word then, and
// only then; every doubt falls back to the full local gate.
//
// The two checks it reads are the pipeline's own: `test` (Linux), and the
// `test-windows` shards (one check run per matrix entry, "test-windows (cli)").
// An OS whose check does not exist on the head is not declared; one that
// exists and is not green is a reason to run locally, never a pass.
const (
	ciReuseKey    = "ci-reuse"
	linuxCheck    = "test"
	windowsPrefix = "test-windows"
)

// CIVerdictCheck is one check run on the PR head: its name, whether it
// completed green, and when it began (zero when GitHub did not say).
type CIVerdictCheck struct {
	Name    string
	Passed  bool
	Started time.Time
}

// CIVerdict is what CI said about a PR: its number, the head commit the
// checks belong to, and every check run on that head.
type CIVerdict struct {
	PR      int
	HeadSHA string
	Checks  []CIVerdictCheck
}

// CIMergeRef is the commit GitHub built for a PR to merge into trunk: the tree
// CI's checkout tested and when the commit was made.
type CIMergeRef struct {
	Tree      string
	Committed time.Time
}

// ciMergeRef fetches refs/pull/<n>/merge. A package var so tests state what
// GitHub answered without a network.
var ciMergeRef = func(dir string, pr int) (CIMergeRef, error) {
	ref := fmt.Sprintf("refs/pull/%d/merge", pr)
	if _, err := git(dir, "fetch", "--quiet", "origin", ref); err != nil {
		return CIMergeRef{}, err
	}
	tree, ok := revTree(dir, "FETCH_HEAD")
	if !ok {
		return CIMergeRef{}, fmt.Errorf("%s has no readable tree", ref)
	}
	out, err := git(dir, "log", "-1", "--format=%ct", "FETCH_HEAD")
	if err != nil {
		return CIMergeRef{}, err
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return CIMergeRef{}, fmt.Errorf("%s has no commit time: %w", ref, err)
	}
	return CIMergeRef{Tree: tree, Committed: time.Unix(secs, 0)}, nil
}

// ReadCIReuse is whether the repo lets the merge gate take CI's verdict. On
// unless aphrollo.toml says `ci-reuse = false`; any other value is refused
// naming the key, since a typo must not silently pick a side.
func ReadCIReuse(root string) (bool, error) {
	v, set := tomlStringIn(root+"/aphrollo.toml", "[aphrollo]", ciReuseKey)
	if !set {
		return true, nil
	}
	switch strings.TrimSpace(v) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%s = %q is not true or false", ciReuseKey, v)
}

// ciOSes names the OSes whose checks all passed, or why the verdict cannot
// stand: the Linux check missing or not green, or a Windows check present and
// not green. started is the earliest begin time among the checks counted.
func ciOSes(checks []CIVerdictCheck) (oses []string, started time.Time, why string) {
	var linux, windows, windowsRed int
	note := func(c CIVerdictCheck) {
		if started.IsZero() || c.Started.Before(started) {
			started = c.Started
		}
	}
	for _, c := range checks {
		switch {
		case c.Name == linuxCheck && c.Passed:
			linux++
			note(c)
		case c.Name == linuxCheck:
			return nil, started, "the Linux check `" + linuxCheck + "` is not green"
		case c.Name == windowsPrefix || strings.HasPrefix(c.Name, windowsPrefix+" ("):
			if !c.Passed {
				windowsRed++
			}
			windows++
			note(c)
		}
	}
	switch {
	case linux == 0:
		return nil, started, "CI has no `" + linuxCheck + "` check on this head"
	case windowsRed > 0:
		return nil, started, "a `" + windowsPrefix + "` check is not green"
	case windows > 0:
		return []string{"linux", "windows"}, started, ""
	}
	return []string{"linux"}, started, ""
}

// ciVerdictTree is the tree the verdict covers when it stands for the merge
// this gate would test, and "" with the reason when it does not.
func ciVerdictTree(laneWorktree string, tips prGateTips, v CIVerdict) (tree string, oses []string, why string) {
	oses, started, why := ciOSes(v.Checks)
	if why != "" {
		return "", nil, why
	}
	if tips.lane != v.HeadSHA {
		return "", nil, "CI's checks are for " + v.HeadSHA + ", not the " + tips.lane + " being merged"
	}
	if strings.TrimSpace(gitOut(laneWorktree, "merge-base", tips.lane, tips.trunk)) == tips.trunk {
		// Trunk is already inside the head: the merge is the head's own tree,
		// whichever trunk commit CI merged it onto.
		tree, ok := revTree(laneWorktree, tips.lane)
		if !ok {
			return "", nil, "the head's tree could not be read"
		}
		return tree, oses, ""
	}
	out, err := git(laneWorktree, "merge-tree", "--write-tree", tips.trunk, tips.lane)
	if err != nil {
		return "", nil, "the merge does not build cleanly"
	}
	merged := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	ref, err := ciMergeRef(laneWorktree, v.PR)
	if err != nil {
		return "", nil, "CI's merge ref could not be read: " + err.Error()
	}
	if ref.Tree != merged {
		return "", nil, "trunk moved since CI tested (its merge tree is " + ref.Tree + ", this one " + merged + ")"
	}
	if started.IsZero() || ref.Committed.After(started) {
		return "", nil, "CI's merge ref is newer than its checks, so they may have tested another merge"
	}
	return merged, oses, ""
}

// GatePRMergeReusingCI is GatePRMerge that takes CI's verdict for the tree
// when it stands for it (ciVerdictTree): the merged tree still has its laws
// judged, but the suites, vet and lint are not run again. Any doubt, a repo
// that turned reuse off, and a repo measuring mutants locally at the merge, run
// the full gate exactly as GatePRMerge does.
func GatePRMergeReusingCI(laneWorktree string, run SuiteRunner, log io.Writer, v CIVerdict) error {
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
	reuse, err := ReadCIReuse(laneWorktree)
	if err != nil {
		return prGateRefusal(laneWorktree, "config", "%v", err)
	}
	if !reuse || (cfg.AtMerge && !cfg.AtMergeCI) {
		return judgeMergedTree(laneWorktree, run, log, nil)
	}
	tips, err := prGateTipsOf(laneWorktree, log)
	if err != nil {
		return err
	}
	if tips.landed {
		return nil
	}
	tree, oses, why := ciVerdictTree(laneWorktree, tips, v)
	if why != "" {
		fmt.Fprintf(log, "gate %s: CI's verdict is not reused (%s); running the local suite\n", premergeDisplayName, why)
		return judgeMergedTree(laneWorktree, run, log, &tips)
	}
	fmt.Fprintf(log, "gate %s: reused CI verdict for tree %s (%s)\n", premergeDisplayName, tree, strings.Join(oses, ", "))
	return judgeMergedTreeWith(laneWorktree, run, log, &tips, MechanicalLaws)
}
