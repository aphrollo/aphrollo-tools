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
//
// Only a check the repo's own workflow published counts: the app must be
// github-actions and the workflow file the bound one (pipeline.yml), since any
// other app can publish a check of the same name. A repo whose jobs are named
// otherwise lists them in `ci-reuse-checks` (the first is required, the others
// count when present) and its workflow file in `ci-reuse-workflow`.
const (
	ciReuseKey         = "ci-reuse"
	ciReuseChecksKey   = "ci-reuse-checks"
	ciReuseWorkflowKey = "ci-reuse-workflow"
	ciReuseApp         = "github-actions"
	defaultWorkflow    = "pipeline.yml"
)

var defaultChecks = []string{"test", "test-windows"}

// osLabels names an OS for the checks the pipeline ships with; any other
// check is named by itself.
var osLabels = map[string]string{"test": "linux", "test-windows": "windows"}

// CIVerdictCheck is one check run on the PR head: its name, whether it
// completed green, when it began (zero when GitHub did not say), the app and
// workflow file that published it, and which attempt of its run it is (0 when
// GitHub did not say).
type CIVerdictCheck struct {
	Name     string
	Passed   bool
	Started  time.Time
	App      string
	Workflow string
	Attempt  int
}

// ciSpec is which checks stand for the suites: the names (first required) and
// the workflow file they must come from.
type ciSpec struct {
	names    []string
	workflow string
}

// readCISpec is the repo's declaration of the bound checks, defaults where it
// declares nothing.
func readCISpec(root string) ciSpec {
	spec := ciSpec{names: defaultChecks, workflow: defaultWorkflow}
	if names := tomlStringsIn(root+"/aphrollo.toml", "[aphrollo]", ciReuseChecksKey); len(names) > 0 {
		spec.names = names
	}
	if w, set := tomlStringIn(root+"/aphrollo.toml", "[aphrollo]", ciReuseWorkflowKey); set && strings.TrimSpace(w) != "" {
		spec.workflow = strings.TrimSpace(w)
	}
	return spec
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

// ciOSes names what ran and passed, or why the verdict cannot stand: the
// required check missing or not green, or another named check present and not
// green. Only checks of the bound app and workflow count. started is the
// earliest begin time among the checks counted; firstAttempts is false when
// any counted check is a re-run or of an attempt GitHub did not name.
func ciOSes(checks []CIVerdictCheck, spec ciSpec) (oses []string, started time.Time, firstAttempts bool, why string) {
	firstAttempts = true
	for i, name := range spec.names {
		var count, red int
		for _, c := range checks {
			if c.App != ciReuseApp || c.Workflow != spec.workflow {
				continue
			}
			if c.Name != name && !strings.HasPrefix(c.Name, name+" (") {
				continue
			}
			count++
			if !c.Passed {
				red++
			}
			if started.IsZero() || c.Started.Before(started) {
				started = c.Started
			}
			if c.Attempt != 1 {
				firstAttempts = false
			}
		}
		switch {
		case red > 0:
			return nil, started, false, "a `" + name + "` check is not green"
		case count == 0 && i == 0:
			return nil, started, false, "CI has no `" + name + "` check of " + spec.workflow + " on this head"
		case count == 0:
			continue
		}
		label := osLabels[name]
		if label == "" {
			label = name
		}
		oses = append(oses, label)
	}
	return oses, started, firstAttempts, ""
}

// ciVerdictTree is the tree the verdict covers when it stands for the merge
// this gate would test, and "" with the reason when it does not.
func ciVerdictTree(laneWorktree string, tips prGateTips, v CIVerdict, spec ciSpec) (tree string, oses []string, why string) {
	oses, started, firstAttempts, why := ciOSes(v.Checks, spec)
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
	// A re-run keeps the merge commit it first tested but starts later, so
	// its start time proves nothing about which merge it saw.
	if !firstAttempts {
		return "", nil, "a counted check is a re-run, or its attempt is unknown, so which merge it tested cannot be named"
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
	tree, oses, why := ciVerdictTree(laneWorktree, tips, v, readCISpec(laneWorktree))
	if why != "" {
		fmt.Fprintf(log, "gate %s: CI's verdict is not reused (%s); running the local suite\n", premergeDisplayName, why)
		return judgeMergedTree(laneWorktree, run, log, &tips)
	}
	note := ""
	if laneChangesOnlyMarkdown(laneWorktree, tips) {
		// CI's test job concludes success with its steps skipped on a diff
		// with no code, so its green says nothing was run.
		note = " — CI ran no tests: non-code diff"
	}
	fmt.Fprintf(log, "gate %s: reused CI verdict for tree %s (%s)%s\n", premergeDisplayName, tree, strings.Join(oses, ", "), note)
	return judgeMergedTreeWith(laneWorktree, run, log, &tips, MechanicalLaws)
}

// laneChangesOnlyMarkdown is whether everything the lane changes against trunk is markdown.
func laneChangesOnlyMarkdown(laneWorktree string, tips prGateTips) bool {
	out := strings.TrimSpace(gitOut(laneWorktree, "diff", "--name-only", tips.trunk+"..."+tips.lane))
	if out == "" {
		return false
	}
	for _, f := range strings.Split(out, "\n") {
		if !strings.HasSuffix(strings.TrimSpace(f), ".md") {
			return false
		}
	}
	return true
}
