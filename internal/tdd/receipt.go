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
//
// One receipt per TREE, named by it. A single well-known file could only ever
// describe the last run on the box, so two lanes measured minutes apart left
// the second one reading the first one's answer and the gate comparing trees
// to notice — a check that reported "wrong tree" for what was really "your
// receipt was overwritten". Keying the FILENAME by the tree it describes
// makes the lookup itself the identity check, and keeps every lane's proof.
type MutationReceipt struct {
	Repo          string `json:"repo"`
	Branch        string `json:"branch"`
	TipTree       string `json:"tip_tree"`
	WorktreeDirty bool   `json:"worktree_dirty"`
	BaseRef       string `json:"base_ref"`
	// BaseSHA is what BaseRef RESOLVED to when the run took its diff. A ref
	// name is not a base: `origin/main` moves, and a receipt measured against
	// yesterday's origin/main mutated different lines than the merge is
	// landing. Empty means an older producer wrote the receipt.
	BaseSHA string `json:"base_sha"`
	MutantsTotal  int    `json:"mutants_total"`
	Caught        int    `json:"caught"`
	Timeout       int    `json:"timeout"`
	Unviable      int    `json:"unviable"`
	Survivors     int    `json:"survivors"`
	Accepted      int    `json:"accepted"`
	// Unaccepted lists the survivors nobody signed off on — a code path no
	// test constrains. Its ENTRIES are opaque here: the producing repo decides
	// how it names a mutant, and a gate that parsed that shape would break the
	// day the shape changed. Non-empty is the whole rule.
	Unaccepted []json.RawMessage `json:"unaccepted"`
	Verdict    string            `json:"verdict"`
	FinishedAt time.Time         `json:"finished_at"`
}

// receiptVerdictPass is the only verdict that merges. Anything else — "fail",
// "error", "skipped", or a spelling this binary has never heard of — refuses,
// because a gate that treats an unrecognised verdict as permission is not a
// gate.
const receiptVerdictPass = "pass"

// MutationReceiptPathFor is where the consuming repo's mutation run leaves the
// receipt for one tree.
func MutationReceiptPathFor(tipTree string) string {
	dir := stateDir()
	if dir == "" || tipTree == "" {
		return ""
	}
	return filepath.Join(dir, "mutation-receipt."+tipTree+".json")
}

// mutationGateHint is the command a rejection points at. It names the
// consuming repo's script, because that is what produces a receipt.
const mutationGateHint = "run tools/mutation_gate.sh on the lane tip (with a clean worktree) and merge again"

// checkMutationReceipt judges the receipt for the tree being merged. It
// returns nil to allow, or a blocking GateResult naming the field that
// failed. tipTree is the LANE TIP's tree (MERGE_HEAD:), never the merge
// result: the merge result has never been mutation-tested by anyone.
// wantBase is the merge base this merge is actually landing against, "" when
// the gate could not name it — in which case it does not judge one.
func checkMutationReceipt(repo, tipTree, wantBase string) *GateResult {
	path := MutationReceiptPathFor(tipTree)
	if path == "" {
		return blockReceipt("no mutation receipt for %s (there is no lane tip to look one up by)", repo)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return blockReceipt("no mutation receipt for %s's lane tip %s at %s", repo, short(tipTree), path)
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return blockReceipt("the mutation receipt at %s is unreadable (%v)", path, err)
	}
	if r.Repo != "" && repo != "" && !strings.EqualFold(r.Repo, repo) {
		return blockReceipt("the receipt for tree %s is for %s, not %s", short(tipTree), r.Repo, repo)
	}
	if r.WorktreeDirty {
		return blockReceipt("worktree_dirty: the run measured uncommitted work, not what is being merged")
	}
	if r.Verdict != receiptVerdictPass {
		return blockReceipt("verdict %q — only %q merges", r.Verdict, receiptVerdictPass)
	}
	if len(r.Unaccepted) > 0 {
		return blockReceipt("%d unaccepted survivor(s), starting with %s — a code path no test constrains",
			len(r.Unaccepted), firstUnaccepted(r.Unaccepted))
	}
	switch {
	case r.BaseSHA == "":
		// An older producer. Accepted, and counted: an unverifiable proof is
		// not the same thing as a verified one, and the tally is how that
		// stops being invisible.
		appendGateLog("premergecommit", logToken(repo), "mutation-receipt", "receipt-unpinned", 0)
	case wantBase != "" && !strings.EqualFold(r.BaseSHA, wantBase):
		return blockReceipt("the receipt was measured against base %s, but this merge lands against %s — a different diff, so different mutants",
			short(r.BaseSHA), short(wantBase))
	}
	return nil
}

// mergeBaseSHA is the commit this merge actually diverged from — what a
// diff-scoped mutation run had to have measured against. "" when git cannot
// say, in which case no base is judged.
func mergeBaseSHA(repoRoot, tipRev string) string {
	if tipRev == "" {
		return ""
	}
	out, err := git(repoRoot, "merge-base", tipRev, "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// firstUnaccepted renders one entry for the rejection line. A survivor written
// as an object is rendered readably; anything else is quoted as it came, so a
// producer that changes the shape still gets a legible message.
func firstUnaccepted(entries []json.RawMessage) string {
	raw := entries[0]
	var s struct {
		File     string `json:"file"`
		Line     int    `json:"line"`
		Mutation string `json:"mutation"`
	}
	if err := json.Unmarshal(raw, &s); err == nil && s.File != "" {
		return fmt.Sprintf("%s:%d (%s)", s.File, s.Line, s.Mutation)
	}
	return strings.TrimSpace(string(raw))
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

// mergeTip is the commit being merged IN — the tree a mutation run measured,
// and a revision the base check can still resolve.
type mergeTip struct {
	Rev, Tree, From string
}

// reflogActionEnv is what git tells a hook it is doing: during a merge it
// reads `merge <ref>`. It is the ONLY signal a clean automerge gives, because
// pre-merge-commit fires BEFORE .git/MERGE_HEAD is written — that file exists
// only for a conflicted or --no-commit merge. Reading it from this process's
// own environment is safe: cleanGitEnv scrubs GIT_* from the CHILD git's
// environment, never from ours.
const reflogActionEnv = "GIT_REFLOG_ACTION"

// mergeTipOf names the lane tip of the merge in progress, preferring
// MERGE_HEAD (when it exists it IS the merge) and falling back to the branch
// git says it is merging.
func mergeTipOf(repoRoot string) (mergeTip, bool) {
	if tree, ok := revTree(repoRoot, "MERGE_HEAD"); ok {
		return mergeTip{Rev: "MERGE_HEAD", Tree: tree, From: "MERGE_HEAD"}, true
	}
	if rev := reflogMergeRev(); rev != "" {
		if tree, ok := revTree(repoRoot, rev); ok {
			return mergeTip{Rev: rev, Tree: tree, From: reflogActionEnv}, true
		}
	}
	return mergeTip{}, false
}

// revTree resolves a revision's tree. `<rev>:` names it, and unlike
// `<rev>^{tree}` it survives any cmd.exe wrapper on the way to git — a caret
// is an escape character there.
func revTree(repoRoot, rev string) (string, bool) {
	out, err := git(repoRoot, "rev-parse", rev+":")
	if err != nil {
		return "", false
	}
	tree := strings.TrimSpace(out)
	return tree, tree != ""
}

// reflogMergeRev is the ref named by `merge <ref>`, "" for any other action.
func reflogMergeRev() string {
	f := strings.Fields(os.Getenv(reflogActionEnv))
	if len(f) < 2 || f[0] != "merge" {
		return ""
	}
	return f[1]
}

// mergeTipTree is the tree of the commit being merged IN, which is what the
// mutation run measured. "" when nothing names a merge.
func mergeTipTree(repoRoot string) string {
	tip, _ := mergeTipOf(repoRoot)
	return tip.Tree
}
