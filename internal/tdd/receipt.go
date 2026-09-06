package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Repo string `json:"repo"`
	// RepoID names the REPOSITORY rather than the directory it was measured
	// in: "root:<sha>" over its root commits, identical in every clone and
	// every worktree on every OS. Repo above is a path, and one repository
	// has as many path spellings as it has checkouts — `D:/…/.git` from
	// Windows, `/mnt/d/…/.git` for the same directory from WSL, and something
	// else again for a Linux-side clone — none of which normalize to each
	// other, so judging by path refused every receipt a Linux run produced
	// (issue #202). Empty means an older producer wrote the receipt, and the
	// path comparison stands in.
	RepoID string `json:"repo_id,omitempty"`
	// Schema is the schema THIS RECEIPT was written at — ReceiptSchemaVersion
	// on whichever binary produced it, stamped by signReceipt/SignReceiptFile
	// so no call site can forget it. It describes the MEASURER, unlike every
	// field above it, which describes the measurement: issue #505 found the
	// receipt's zero_reason field indistinguishable, in an old receipt, from
	// a current producer that simply left it empty — every field here was
	// omitempty, so "wrote before the field existed" and "wrote after, with
	// nothing to say" were the identical bytes.
	//
	// Absent (0) means either of those, still: this field cannot retroactively
	// fix a receipt that already exists on disk without it, only the NEXT
	// field the receipt grows — a reader can finally say "older producer"
	// instead of guessing, but only once ReceiptSchemaVersion has moved past
	// the value this field would need to name. See checkReceiptSchema and
	// checkReceiptNotVacuous's use of it.
	Schema        int    `json:"schema,omitempty"`
	Branch        string `json:"branch"`
	TipTree       string `json:"tip_tree"`
	WorktreeDirty bool   `json:"worktree_dirty"`
	BaseRef       string `json:"base_ref"`
	// BaseSHA is what BaseRef RESOLVED to when the run took its diff. A ref
	// name is not a base: `origin/main` moves, and a receipt measured against
	// yesterday's origin/main mutated different lines than the merge is
	// landing. Empty means an older producer wrote the receipt.
	BaseSHA string `json:"base_sha"`
	// MovedLines is how many diff lines git judged to be MOVED and this run
	// therefore never mutated. A crate-topology lane moves code byte for
	// byte, and mutating a moved line measures nothing; the count is here so
	// a zero-mutant receipt says why it is zero.
	MovedLines int `json:"moved_lines,omitempty"`
	// ZeroReason is the producer's own explanation for why MutantsTotal and
	// MovedLines are both zero — a fact only the producer can know, because
	// it comes from CONTENT (what cargo-mutants' own mutators found in the
	// diff), never from the PATH-based guess the gate would otherwise have
	// to make and keep re-guessing (issue #494: a deleted source file and a
	// comment-only edit each look mutable by path and are not, by content).
	// Empty means the producer gave no reason, judged exactly as before this
	// field existed: see checkReceiptNotVacuous, which refuses that case
	// precisely because an unexplained zero and a wrong-base zero are
	// otherwise indistinguishable from here. See
	// ReceiptZeroReasonNoMutableSource for the one value this binary itself
	// ever writes.
	ZeroReason   string `json:"zero_reason,omitempty"`
	MutantsTotal int    `json:"mutants_total"`
	Caught       int    `json:"caught"`
	Timeout      int    `json:"timeout"`
	Unviable     int    `json:"unviable"`
	// NotCovered is how many mutants the runner never ran a test for. They are
	// neither caught nor survived: nothing was measured. Counted so a receipt
	// says how much of its diff went unproven rather than implying the whole
	// of it was judged.
	NotCovered int `json:"not_covered,omitempty"`
	// Excluded is how many tests the repo's own mutation-baseline-exclude
	// declared out of measurement, from the baseline and from mutant testing
	// alike (issue #265). Zero for a repo that declares none, which is the
	// whole point: the mitigation for a repo quietly excluding its way to a
	// green receipt is that the count is visible in the artefact a merge
	// reads, not only in config.
	Excluded int `json:"excluded,omitempty"`
	// Survivors and Unaccepted are LISTS of mutants, as the producer writes
	// them — the count is len(). Declaring survivors an int is what made
	// every merge die on "cannot unmarshal array into Go struct field"; then
	// declaring the entries strings died the same way on the first receipt
	// with one accepted survivor, because tools/mutation_gate.sh writes each
	// as {"file","line","mutation"}. MutantName takes either spelling. A
	// non-empty Unaccepted is the whole rule.
	Survivors []MutantName `json:"survivors"`
	Accepted  int          `json:"accepted"`
	// AcceptKindCounts splits Accepted by claim (issue #268): closed
	// equivalence versus the two kinds of parked debt. Zero fields for a
	// producer that predates it, same as every other omitempty count here.
	AcceptKindCounts
	Unaccepted []MutantName `json:"unaccepted"`
	Verdict    string       `json:"verdict"`
	FinishedAt time.Time    `json:"finished_at"`
	// Outcomes is every mutant the run measured, each carrying the file blob
	// and package test-set hash it was measured against — what makes the NEXT
	// run incremental (see mutants_plan.go). A producer that has not caught up
	// writes none, which costs a full re-run and nothing else.
	Outcomes []MutantOutcome `json:"outcomes,omitempty"`
	// Files is the blob hash of every file the run's diff covered, and
	// Fences the invalidation hash of every package. Together they are what
	// the NEXT run narrows its diff with (see PlanDiffFiles); absent, it
	// measures everything.
	Files  map[string]string `json:"files,omitempty"`
	Fences map[string]string `json:"fences,omitempty"`
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

// ReceiptSchemaVersion is the schema this binary stamps into every receipt it
// signs (signReceipt, SignReceiptFile). It is its OWN version, deliberately
// separate from StateSchema (stateschema.go) and mutantOutcomeSchema
// (mutants_store.go): those version a whole-file shape a reader either
// understands or does not, where the receipt already has a per-FIELD
// backward-compatibility mechanism (receiptFieldPresence) that has served it
// fine for every field before this one. What per-field presence cannot say is
// "an older producer wrote this receipt", which is exactly the fact issue
// #505 needed and could not get: a receipt from before ZeroReason existed and
// one from after that leaves it empty are the same bytes. This field is
// deliberately for the NEXT such gap, not this one — see MutationReceipt.Schema.
//
// Bumped only when a reader needs to tell an older producer's receipt apart
// from a current one's, i.e. when the ambiguity ZeroReason hit recurs for a
// new field.
const ReceiptSchemaVersion = 1

// ReceiptZeroReasonNoMutableSource is the one ZeroReason value this binary
// itself ever writes: the producer's own tool (cargo-mutants) reported that
// the diff it was given carries no mutable Rust source — a deletion, or an
// edit confined to a comment, whitespace or a string literal, none of which
// any mutator touches. A producer's own reason string, once it writes one
// directly, is honoured verbatim; this binary never manufactures a different
// value.
const ReceiptZeroReasonNoMutableSource = "no_mutable_source"

// MutationReceiptPathFor is where the consuming repo's mutation run leaves the
// receipt for one tree.
func MutationReceiptPathFor(tipTree string) string {
	dir := stateDir()
	if dir == "" || tipTree == "" {
		return ""
	}
	return filepath.Join(dir, "mutation-receipt."+tipTree+".json")
}

// mutationGateHint is the command a rejection points at, named through the
// SAME lookup missingReceiptRemedy uses (mutantsRunnerCommand) — issue #141:
// every refusal but the missing-receipt one used to hard-code
// tools/mutation_gate.sh regardless of root, so a Go-only repo hitting a
// dirty-worktree or bad-verdict refusal was told to run a script it does not
// have.
func mutationGateHint(root string) string {
	// The command to TYPE, not the producer it drives: naming the producer as
	// the remedy sent every session that read this refusal to the script by
	// hand, outside the lock the script is supposed to run under.
	return "run `aphrollo gate mutants run` on the lane tip (with a clean worktree) and merge again" + drivesClause(root)
}

// receiptRejectionMarker is the sentence every blockReceipt message carries
// regardless of root — the part isReceiptRejection reads, since the hint
// half now varies by repo.
const receiptRejectionMarker = "the receipt proves it constrains behaviour"

// receiptContext is what the merge in progress knows about the lane being
// merged: where the checkout is, which repo it belongs to, the LANE TIP's
// tree (MERGE_HEAD:, never the merge result — the merge result has never been
// mutation-tested by anyone), and the merge base the lane lands against (""
// when the gate could not name it, in which case it judges no base).
type receiptContext struct {
	RepoRoot string
	Repo     string
	// RepoID is this checkout's repository identity, the same string the
	// producer wrote into the receipt. See MutationReceipt.RepoID.
	RepoID  string
	TipTree string
	BaseSHA string
}

// newReceiptContext describes the merge in progress to the receipt check.
//
// Repo is the repo's shared git COMMON dir, never repoRoot's own directory
// name: a linked worktree is routinely named unlike the repo (a lane checked
// out at `.worktrees/borld/eol`), but every worktree of one repo shares that
// one directory. RepoID is the location-independent identity that outlives a
// move between checkouts and operating systems; see MutationReceipt.RepoID.
func newReceiptContext(repoRoot string, tip mergeTip) receiptContext {
	return receiptContext{
		RepoRoot: repoRoot,
		Repo:     commonGitDir(repoRoot),
		RepoID:   repoIdentity(repoRoot),
		TipTree:  tip.Tree,
		BaseSHA:  mergeBaseSHA(repoRoot, tip.Rev),
	}
}

// checkMutationReceipt judges the receipt for the tree being merged. It
// returns nil to allow, or a blocking GateResult naming the field that
// failed.
func checkMutationReceipt(ctx receiptContext) *GateResult {
	tipTree, repo := ctx.TipTree, ctx.Repo
	path := MutationReceiptPathFor(tipTree)
	if path == "" {
		return blockReceipt(ctx.RepoRoot, "no-tip-tree", "no mutation receipt for %s (there is no lane tip to look one up by)", repo)
	}
	if laneHasNothingToMutate(ctx) {
		appendGateLog(premergeLogToken, logToken(repo), "mutation-receipt", "receipt-not-required:"+short(tipTree), 0)
		return nil
	}
	data, err := os.ReadFile(path)
	carried := false
	if err != nil {
		c, ok := carryReceiptForward(ctx)
		if !ok {
			return blockMissingReceipt(ctx)
		}
		data, carried = c, true
	}
	// Before a single field is believed: a receipt nothing measured is not a
	// weaker proof, it is somebody's typing.
	if res := verifyReceiptMAC(data, repo, tipTree); res != nil {
		return res
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return blockReceipt(ctx.RepoRoot, "unreadable", "the mutation receipt at %s is unreadable (%v)", path, err)
	}
	// Before any other field on it is trusted: a schema newer than this
	// binary understands may carry meanings the checks below cannot see.
	if res := checkReceiptSchema(ctx.RepoRoot, r); res != nil {
		return res
	}
	// Fields as the WIRE bytes actually carried them, not the Go zero value a
	// field an older producer never wrote is indistinguishable from — see
	// checkReceiptUnacceptedCoherence and checkReceiptCountCoherence.
	present := receiptFieldPresence(data)
	if res := checkReceiptUnacceptedCoherence(ctx.RepoRoot, present, r); res != nil {
		return res
	}
	if res := judgeReceiptRepo(r, ctx); res != nil {
		return res
	}
	if r.WorktreeDirty {
		return blockReceipt(ctx.RepoRoot, "worktree-dirty", "worktree_dirty: the run measured uncommitted work, not what is being merged")
	}
	if r.Verdict != receiptVerdictPass {
		return blockReceipt(ctx.RepoRoot, "bad-verdict", "verdict %q — only %q merges", r.Verdict, receiptVerdictPass)
	}
	if len(r.Unaccepted) > 0 {
		return blockReceipt(ctx.RepoRoot, "unaccepted-survivor", "%d unaccepted survivor(s), starting with %s — a code path no test constrains",
			len(r.Unaccepted), firstUnaccepted(r.Unaccepted))
	}
	if r.Timeout > 0 {
		return blockReceipt(ctx.RepoRoot, "timeout", "%s", mutantsTimedOutLine(r.Timeout))
	}
	if res := checkReceiptCountCoherence(ctx.RepoRoot, present, r); res != nil {
		return res
	}
	if res := checkReceiptNotVacuous(ctx.RepoRoot, present, r); res != nil {
		return res
	}
	switch {
	case r.BaseSHA == "":
		// An older producer. Accepted, and counted: an unverifiable proof is
		// not the same thing as a verified one, and the tally is how that
		// stops being invisible.
		appendGateLog(premergeLogToken, logToken(repo), "mutation-receipt", "receipt-unpinned", 0)
	case ctx.BaseSHA != "" && !strings.EqualFold(r.BaseSHA, ctx.BaseSHA):
		return blockReceipt(ctx.RepoRoot, "base-mismatch", "the receipt was measured against base %s, but this merge lands against %s — a different diff, so different mutants",
			short(r.BaseSHA), short(ctx.BaseSHA))
	}
	// Every other outcome this stage can reach leaves a line — not-required,
	// carried, rejected, forged, unsigned, unverifiable, measured-in-ci — but
	// a plain accept left none at all, so an audit could not tell "this
	// merge's receipt passed" from "this stage never ran" (issue #136). The
	// carried case already logged its own line (receipt-carried) naming
	// where the proof came from; this one is the direct read, kept as its
	// own count rather than folded into carried's.
	if !carried {
		// gate.log is space-separated (see appendGateLog), so the verdict is
		// ONE token: underscores stand in for the spaces the issue's own
		// wording uses.
		appendGateLog(premergeLogToken, logToken(repo), "mutation-receipt",
			fmt.Sprintf("receipt-accepted:%s_caught=%d_missed=%d_accepted=%d", short(tipTree), r.Caught, len(r.Survivors), r.Accepted), 0)
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
// judgeReceiptRepo decides whether this receipt was measured in the
// repository being merged. Identity decides whenever both sides carry one,
// because it survives the move between checkouts and operating systems that
// a path spelling cannot; the path comparison stands in only for a receipt
// written before repo_id existed, so proofs already on the box keep merging.
func judgeReceiptRepo(r MutationReceipt, ctx receiptContext) *GateResult {
	if r.RepoID != "" && ctx.RepoID != "" {
		if !strings.EqualFold(r.RepoID, ctx.RepoID) {
			return blockReceipt(ctx.RepoRoot, "repo-mismatch", "the receipt for tree %s was measured in repository %s, and this merge is landing in %s",
				short(ctx.TipTree), r.RepoID, ctx.RepoID)
		}
		return nil
	}
	if r.Repo != "" && ctx.Repo != "" && !sameRepo(r.Repo, ctx.Repo) {
		return blockReceipt(ctx.RepoRoot, "repo-mismatch", "the receipt for tree %s is for %s, not %s", short(ctx.TipTree), r.Repo, ctx.Repo)
	}
	return nil
}

// repoIdentity names repoRoot's REPOSITORY, independent of where it is
// checked out: the sorted root commits of its history, which every clone and
// every worktree of that repository shares and no other repository has.
// "" when git cannot say, in which case the caller falls back to the path.
func repoIdentity(repoRoot string) string {
	out, err := git(repoRoot, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return ""
	}
	roots := strings.Fields(out)
	if len(roots) == 0 {
		return ""
	}
	sort.Strings(roots)
	return "root:" + strings.Join(roots, ",")
}

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
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Col tells apart the several distinct mutants cargo-mutants emits on one
	// line with identical text.
	Col      int    `json:"col,omitempty"`
	Mutation string `json:"mutation,omitempty"`
	// Raw is the bare-string spelling, kept verbatim.
	Raw string `json:"-"`
}

// key identifies the mutant this name refers to, the same way an outcome
// does, so a survivor list and an outcome list can be compared.
func (m MutantName) key() mutantKey {
	return mutantKey{File: m.File, Line: m.Line, Col: m.Col, Mutation: m.Mutation}
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
	appendGateLog(premergeLogToken, logToken(ctx.Repo), "mutation-receipt", "receipt-rejected:missing", 0)
	return &GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate: %s %s — %s", missingReceiptMarker, short(ctx.TipTree), missingReceiptRemedy(ctx))}
}

// missingReceiptMarker is the phrase that identifies this rejection, the way
// receiptRejectionMarker identifies the other half of the family. The escape
// recorder reads both: a merge refused for want of a receipt is the gate
// working, not evidence about the pre-commit gate, which has no receipt stage
// to have missed.
const missingReceiptMarker = "mutation receipt missing for tree"

// missingReceiptRemedy is the second half of that line. A run that is ALREADY
// going is the remedy: told only to run the script, a session starts a second
// mutation run on top of the first, which is how a box ends up with two
// multi-hour builds fighting for the same cores.
//
// The job named here is the one that would actually WRITE ctx.TipTree's
// receipt — matchingRunningJob, not "whichever job is newest in the repo's
// registry" (issue #431): a repo with several lanes running at once has
// several live jobs, and naming the wrong one reads as "your receipt is
// minutes away" when the run that will produce it started much earlier and
// is queued behind the one actually holding the box-wide lock. The line
// itself is FormatMutantsStatus's own MutantsRunGoing rendering — the same
// one `aphrollo gate mutants status` prints for this exact state — so this
// message and that command can never describe the same run two ways.
func missingReceiptRemedy(ctx receiptContext) string {
	if j, ok := matchingRunningJob(ctx.Repo, ctx.TipTree); ok {
		rep := MutantsStatusReport{
			Branch: j.Branch, TipTree: ctx.TipTree,
			State: MutantsRunGoing, JobPID: j.PID, JobStarted: j.Started,
		}
		if owner, held := readBuildLockOwnerAt(mutantsRunLockOwnerPath()); held && owner.PID != j.PID {
			rep.WaitingOnLock = true
			rep.LockHolder = describeOwner(owner)
		}
		line, _ := FormatMutantsStatus(rep)
		return strings.TrimPrefix(line, mutantsStatusLinePrefix)
	}
	// A run that ENDED without a receipt is the third answer, and the one a
	// session cannot work out for itself: "run it again" is wrong advice when
	// the last run died — or its own tip was rewritten out from under it
	// (issue #367) — and the reason is already written down.
	if d, ok := loadMutantsDeath(ctx.TipTree); ok {
		return mutantsDeathRemedyLine(d)
	}
	// No base to name: `run` derives it from the lane itself (laneBaseSHA),
	// which is the same base this gate checks the receipt against. A base
	// spelled out here for the caller to retype is a base the two can
	// disagree about.
	return "run `aphrollo gate mutants run` in the lane" + drivesClause(ctx.RepoRoot)
}

// mutantsRunnerCommand names the command that would actually produce a
// receipt for root: the repo's own declared name (`mutation-runner` under
// `[workspace.metadata.aphrollo]` in Cargo.toml, or `[aphrollo]` in
// aphrollo.toml), `tools/mutation_gate.sh` only when the repo genuinely
// carries one, and this binary's own runner otherwise — never a script name
// the repo does not have.

// blockReceipt refuses a merge over the receipt stage. root is the repo
// checkout the hint is named for — every refusal now routes through the same
// mutantsRunnerCommand lookup missingReceiptRemedy uses, so a Go-only repo
// hitting a dirty-worktree, bad-verdict, wrong-repo, unaccepted-survivor or
// base-mismatch refusal is never told to run tools/mutation_gate.sh, a
// script it does not have (issue #141). reason is a fixed short name for
// WHICH cause fired, logged as receipt-rejected:<reason> before the message
// is built — required, not optional, so a new call site cannot forget it and
// land back on one undifferentiated counter (issue #376).
func blockReceipt(root, reason, format string, args ...any) *GateResult {
	appendGateLog(premergeLogToken, logToken(root), "mutation-receipt", "receipt-rejected:"+reason, 0)
	return &GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate premerge: %s. Fail-first proves a test failed once; the receipt proves it constrains behaviour — %s.",
		fmt.Sprintf(format, args...), mutationGateHint(root))}
}

// mutantsTimedOutLine is the one sentence every judge prints for a timeout,
// so the CI check and the merge gate say the same thing about the same fact.
//
// A timeout is an UNMEASURED mutant filed beside the measured ones. Nine were
// measured on one lane at cargo-mutants' 30 s default while eight cold tree
// copies were compiling: the suite was fine and the box was busy, and the
// receipt reported it as a result.
func mutantsTimedOutLine(n int) string {
	return fmt.Sprintf("%d mutant(s) timed out — an unmeasured mutant is not a result: rerun with fewer jobs", n)
}
