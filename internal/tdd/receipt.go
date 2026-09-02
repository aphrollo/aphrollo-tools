package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The fail-first gate proves a test FAILED before it passed. A test that
// asserts nothing satisfies that perfectly, which is the hole the mutation
// receipt closes: fail-first says "it failed once", the receipt says "it
// constrains behaviour", and a merge needs both. The receipt is written by
// the consuming repo's own mutation run (borld: tools/mutation_gate.sh),
// because only that repo knows which mutants are worth generating.
type MutationReceipt struct {
	Repo          string             `json:"repo"`
	Branch        string             `json:"branch"`
	TipTree       string             `json:"tip_tree"`
	WorktreeDirty bool               `json:"worktree_dirty"`
	BaseRef       string             `json:"base_ref"`
	MutantsTotal  int                `json:"mutants_total"`
	Caught        int                `json:"caught"`
	Survivors     []MutationSurvivor `json:"survivors"`
	Accepted      int                `json:"accepted"`
	FinishedAt    time.Time          `json:"finished_at"`
}

// MutationSurvivor is one mutant no test killed: a code path nothing
// constrains.
type MutationSurvivor struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Mutation string `json:"mutation"`
}

// MutationReceiptPath is where the consuming repo's mutation run leaves its
// receipt.
func MutationReceiptPath() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "mutation-receipt.json")
}

// mutationGateHint is the command a rejection points at. It names the
// consuming repo's script, because that is what produces a receipt.
const mutationGateHint = "run tools/mutation_gate.sh on the lane tip (with a clean worktree) and merge again"

// checkMutationReceipt judges the receipt against the tree being merged. It
// returns nil to allow, or a blocking GateResult naming the field that
// failed. tipTree is the LANE TIP's tree (MERGE_HEAD^{tree}), never the merge
// result: the merge result has never been mutation-tested by anyone.
func checkMutationReceipt(repo, tipTree string) *GateResult {
	path := MutationReceiptPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return blockReceipt("no mutation receipt for %s at %s", repo, path)
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return blockReceipt("the mutation receipt at %s is unreadable (%v)", path, err)
	}
	if r.Repo != "" && repo != "" && !strings.EqualFold(r.Repo, repo) {
		return blockReceipt("no mutation receipt for %s (the one on disk is for %s)", repo, r.Repo)
	}
	if r.TipTree != tipTree {
		return blockReceipt("tip_tree %s describes a different tree than the lane tip %s", short(r.TipTree), short(tipTree))
	}
	if r.WorktreeDirty {
		return blockReceipt("worktree_dirty: the run measured uncommitted work, not what is being merged")
	}
	if len(r.Survivors) > r.Accepted {
		return blockReceipt("%d survivors with %d accepted — %s is a code path no test constrains",
			len(r.Survivors), r.Accepted, survivorName(r.Survivors))
	}
	return nil
}

func survivorName(s []MutationSurvivor) string {
	if len(s) == 0 {
		return "a mutant"
	}
	return fmt.Sprintf("%s:%d (%s)", s[0].File, s[0].Line, s[0].Mutation)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "(none)"
	}
	return sha
}

func blockReceipt(format string, args ...any) *GateResult {
	return &GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate premergecommit: %s. Fail-first proves a test failed once; the receipt proves it constrains behaviour — %s.",
		fmt.Sprintf(format, args...), mutationGateHint)}
}

// mergeTipTree is the tree of the commit being merged IN, which is what the
// mutation run measured. "" when there is no merge in progress.
func mergeTipTree(repoRoot string) string {
	out, err := git(repoRoot, "rev-parse", "MERGE_HEAD^{tree}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
