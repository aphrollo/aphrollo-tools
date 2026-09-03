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
	BaseSHA      string `json:"base_sha"`
	MutantsTotal int    `json:"mutants_total"`
	Caught       int    `json:"caught"`
	Timeout      int    `json:"timeout"`
	Unviable     int    `json:"unviable"`
	// Survivors and Unaccepted are LISTS of mutants, as the producer writes
	// them — the count is len(). Declaring survivors an int is what made
	// every merge die on "cannot unmarshal array into Go struct field"; then
	// declaring the entries strings died the same way on the first receipt
	// with one accepted survivor, because tools/mutation_gate.sh writes each
	// as {"file","line","mutation"}. MutantName takes either spelling. A
	// non-empty Unaccepted is the whole rule.
	Survivors  []MutantName `json:"survivors"`
	Accepted   int          `json:"accepted"`
	Unaccepted []MutantName `json:"unaccepted"`
	Verdict    string       `json:"verdict"`
	FinishedAt time.Time    `json:"finished_at"`
	// Outcomes is every mutant the run measured, each carrying the file blob
	// and package test-set hash it was measured against — what makes the NEXT
	// run incremental (see mutants_plan.go). A producer that has not caught up
	// writes none, which costs a full re-run and nothing else.
	Outcomes []MutantOutcome `json:"outcomes,omitempty"`
	// Files is the blob hash of every file the run's diff covered, and
	// TestSets the hash of every package's test files. Together they are what
	// the NEXT run narrows its diff with (see PlanDiffFiles); absent, it
	// measures everything.
	Files    map[string]string `json:"files,omitempty"`
	TestSets map[string]string `json:"test_sets,omitempty"`
	// CarriedFrom names the tree whose run this receipt re-stamps, "" for a
	// receipt that measured its own tree.
	CarriedFrom string `json:"carried_from,omitempty"`
	// MAC is the signature over this receipt's canonical body, written ONLY
	// by `aphrollo gate receipt sign`, and OutcomesSHA the hash of the run's
	// own outcome file — the evidence the receipt was taken from. Both empty
	// for a producer that has not caught up, which is counted, not refused.
	MAC         string `json:"mac,omitempty"`
	OutcomesSHA string `json:"outcomes_sha,omitempty"`
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

// receiptContext is what the merge in progress knows about the lane being
// merged: where the checkout is, which repo it belongs to, the LANE TIP's
// tree (MERGE_HEAD:, never the merge result — the merge result has never been
// mutation-tested by anyone), and the merge base the lane lands against (""
// when the gate could not name it, in which case it judges no base).
type receiptContext struct {
	RepoRoot string
	Repo     string
	TipTree  string
	BaseSHA  string
}

// checkMutationReceipt judges the receipt for the tree being merged. It
// returns nil to allow, or a blocking GateResult naming the field that
// failed.
func checkMutationReceipt(ctx receiptContext) *GateResult {
	tipTree, repo := ctx.TipTree, ctx.Repo
	path := MutationReceiptPathFor(tipTree)
	if path == "" {
		return blockReceipt("no mutation receipt for %s (there is no lane tip to look one up by)", repo)
	}
	if laneHasNothingToMutate(ctx) {
		appendGateLog("premergecommit", logToken(repo), "mutation-receipt", "receipt-not-required:"+short(tipTree), 0)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if carried, ok := carryReceiptForward(ctx); ok {
			data = carried
		} else {
			return blockMissingReceipt(ctx)
		}
	}
	// Before a single field is believed: a receipt nothing measured is not a
	// weaker proof, it is somebody's typing.
	if res := verifyReceiptMAC(data, repo, tipTree); res != nil {
		return res
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return blockReceipt("the mutation receipt at %s is unreadable (%v)", path, err)
	}
	if r.Repo != "" && repo != "" && !sameRepo(r.Repo, repo) {
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
	if r.Timeout > 0 {
		// A timeout is an UNMEASURED mutant filed beside the measured ones.
		// Nine were measured on one lane at cargo-mutants' 30 s default while
		// eight cold tree copies were compiling: the suite was fine and the
		// box was busy, and the receipt reported it as a result.
		return blockReceipt("%d mutant(s) timed out — an unmeasured mutant is not a result: rerun with fewer jobs",
			r.Timeout)
	}
	switch {
	case r.BaseSHA == "":
		// An older producer. Accepted, and counted: an unverifiable proof is
		// not the same thing as a verified one, and the tally is how that
		// stops being invisible.
		appendGateLog("premergecommit", logToken(repo), "mutation-receipt", "receipt-unpinned", 0)
	case ctx.BaseSHA != "" && !strings.EqualFold(r.BaseSHA, ctx.BaseSHA):
		return blockReceipt("the receipt was measured against base %s, but this merge lands against %s — a different diff, so different mutants",
			short(r.BaseSHA), short(ctx.BaseSHA))
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

// sameRepo compares two spellings of one repo's git COMMON dir — the ONE
// directory every worktree of a repo shares, which is what actually
// identifies "one repo" regardless of which worktree's own folder a merge
// happens to run in. The producer names it however its own script does
// (borld's writes `git rev-parse --git-common-dir` directly, e.g.
// `D:/Projects/borld/.git`); the gate resolves the SAME thing for the
// checkout doing the merge (commonGitDir) and compares the two full paths,
// normalized. A basename-only comparison used to reduce both sides to their
// trailing folder name, which had two bugs at once: a linked worktree named
// unlike the repo (`.worktrees/borld/eol`) was wrongly REFUSED a receipt the
// main checkout wrote, and two unrelated repos that happened to share a
// folder name would have been wrongly ACCEPTED as the same one.
func sameRepo(a, b string) bool {
	return strings.EqualFold(normalizeRepoSpelling(a), normalizeRepoSpelling(b))
}

// normalizeRepoSpelling reduces one spelling of a repo's git directory to a
// canonical, comparable form: forward slashes, no trailing slash, and — per
// the fix's spec — a spelling that already names the `.git` dir is accepted
// as is, while a bare repo ROOT (no `.git` suffix) gets `.git` appended, so
// both ways of naming the same directory converge on the same string.
// Casing is left alone here; the caller compares case-insensitively (the
// same checkout routinely appears under two Windows drive-letter casings).
func normalizeRepoSpelling(s string) string {
	s = strings.TrimRight(strings.ReplaceAll(s, `\`, "/"), "/")
	if s == "" {
		return s
	}
	if strings.EqualFold(s, ".git") || strings.HasSuffix(strings.ToLower(s), "/.git") {
		return s
	}
	return s + "/.git"
}

// commonGitDir resolves repoRoot's shared git directory — `git rev-parse
// --git-common-dir`, absolute and forward-slashed — the one directory EVERY
// worktree of repoRoot's repo shares, unlike `--git-dir` which names the
// invoking worktree's own (private) one. "" when git cannot say (not a
// repo, or git itself failed); checkMutationReceipt already treats an empty
// repo string as "skip the repo check" rather than a mismatch.
func commonGitDir(repoRoot string) string {
	out, err := git(repoRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// MutantName is one survivor as the producer named it: an object
// {"file","line","mutation"} from tools/mutation_gate.sh, or a bare string
// from an older producer. Decoding accepts both; String renders both as
// file:line: mutation so a rejection names a place to go.
type MutantName struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Mutation string `json:"mutation,omitempty"`
	// Raw is the bare-string spelling, kept verbatim.
	Raw string `json:"-"`
}

func (m *MutantName) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &m.Raw)
	}
	type object MutantName
	var o object
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("survivor entry %s: %w", b, err)
	}
	*m = MutantName(o)
	return nil
}

func (m MutantName) MarshalJSON() ([]byte, error) {
	if m.Raw != "" {
		return json.Marshal(m.Raw)
	}
	type object MutantName
	return json.Marshal(object(m))
}

func (m MutantName) String() string {
	if m.Raw != "" {
		return strings.TrimSpace(m.Raw)
	}
	return fmt.Sprintf("%s:%d: %s", m.File, m.Line, m.Mutation)
}

// firstUnaccepted is the one entry the rejection line quotes, exactly as the
// producer named it.
func firstUnaccepted(entries []MutantName) string {
	return entries[0].String()
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

// blockMissingReceipt is the rejection for a tip nothing has measured. It is
// ONE line and it ends in the one thing that would fix it, because that is
// what a session at a blocked merge needs — the paragraph explaining what a
// receipt is belongs to the rules, not to every rejection.
func blockMissingReceipt(ctx receiptContext) *GateResult {
	return &GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate: mutation receipt missing for tree %s — %s", short(ctx.TipTree), missingReceiptRemedy(ctx))}
}

// missingReceiptRemedy is the second half of that line. A run that is ALREADY
// going is the remedy: told only to run the script, a session starts a second
// mutation run on top of the first, which is how a box ends up with two
// multi-hour builds fighting for the same cores.
func missingReceiptRemedy(ctx receiptContext) string {
	if jobs := RunningMutantsJobs(ctx.Repo); len(jobs) > 0 {
		j := jobs[len(jobs)-1]
		return fmt.Sprintf("running since %s (pid %d)", j.Started.Format("15:04"), j.PID)
	}
	// A run that ENDED without a receipt is the third answer, and the one a
	// session cannot work out for itself: "run it again" is wrong advice when
	// the last run died, and the reason is already written down.
	if d, ok := loadMutantsDeath(ctx.TipTree); ok {
		return fmt.Sprintf("the run died (exit %d) at %s — see %s", d.Exit, d.At.Format("15:04"), d.ErrLog)
	}
	return "run tools/mutation_gate.sh main"
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
