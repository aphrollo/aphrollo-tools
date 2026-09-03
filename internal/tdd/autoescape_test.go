package tdd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- the channel: a trailer on the commit the gate passed -------------------

// gitOutT is a git value a test needs to compare against.
func gitOutT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// CI runs on a box that has never seen this machine's gate state, so the only
// thing that can tell it "the local gate passed on this exact tree" is
// something carried BY the commit. The trailer is that channel.
func TestCommitMsgAppendsTheGreenGateTrailerForTheTreeThePrecommitPassed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampPrecommitGreen(root)

	msgPath := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(msgPath, []byte("Add the next thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	AppendGateTrailer(root, msgPath)

	data, err := os.ReadFile(msgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := gateGreenTrailer(gitOutT(t, root, "write-tree"))
	if !strings.Contains(string(data), want) {
		t.Fatalf("the message must carry %q:\n%s", want, data)
	}
}

// A commit the gate never judged must not claim it did — that claim is the
// whole basis on which CI later calls a failure an escape.
func TestCommitMsgAddsNoTrailerForATreeThePrecommitNeverPassed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")

	msgPath := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(msgPath, []byte("Add the next thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	AppendGateTrailer(root, msgPath)

	data, _ := os.ReadFile(msgPath)
	if strings.Contains(string(data), gateTrailerKey) {
		t.Fatalf("an unjudged tree must carry no gate trailer:\n%s", data)
	}
}

// The stamp is per TREE: a stamp from an earlier commit must not vouch for
// the one being written now.
func TestGateTrailerIsNotWrittenForADifferentTreeThanTheOneStamped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampPrecommitGreen(root)
	// Stage something else: the tree the gate passed is no longer the tree
	// about to be committed.
	write(t, root, "b.go", "package m\n")
	gitDo(t, root, "add", ".")

	msgPath := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(msgPath, []byte("Add b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	AppendGateTrailer(root, msgPath)

	data, _ := os.ReadFile(msgPath)
	if strings.Contains(string(data), gateTrailerKey) {
		t.Fatalf("a stamp for another tree must not vouch for this one:\n%s", data)
	}
}

// --- the fingerprint dedupe -------------------------------------------------

// The same failing stage repeating every merge must be ONE issue. Without a
// dedupe the automatic recorder turns a recurring red into a stream of
// identical issues, which is worse than recording nothing.
func TestASecondIdenticalEscapeWithinTheWindowRecordsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	o := EscapeOptions{Reason: "the merge gate refused a green lane", Evidence: "clippy: unused variable"}

	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); !recorded {
		t.Fatal("the first sighting must be recorded")
	}
	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); recorded {
		t.Fatal("the same stage and diagnostic inside the window must record nothing")
	}
	if n := len(readEscapes(t)); n != 1 {
		t.Fatalf("escapes.jsonl holds %d records, want 1", n)
	}
}

// Seven days on, the same class escaping AGAIN is news: it means the first
// issue never closed the hole.
func TestTheSameEscapeAfterTheWindowIsRecordedAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	o := EscapeOptions{Reason: "the merge gate refused a green lane", Evidence: "clippy: unused variable"}
	stale := EscapeRecord{
		Schema:      StateSchema,
		ID:          "old",
		Kind:        EscapeKind,
		Reason:      o.Reason,
		At:          time.Now().UTC().Add(-escapeDedupeWindow - time.Hour),
		Fingerprint: escapeFingerprint("merge:premergecommit", o),
	}
	if err := appendEscape(stale); err != nil {
		t.Fatal(err)
	}

	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); !recorded {
		t.Fatal("past the window the same class must be recorded again")
	}
}

// A CLOSED record is a hole somebody fixed. If it reopens, that is the
// loudest signal the loop produces and must never be swallowed by the window.
func TestAClosedEscapeDoesNotSuppressItsRecurrence(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	o := EscapeOptions{Reason: "the merge gate refused a green lane"}
	closed := EscapeRecord{
		Schema:      StateSchema,
		ID:          "fixed",
		Kind:        EscapeKind,
		Reason:      o.Reason,
		At:          time.Now().UTC(),
		Closed:      true,
		Fingerprint: escapeFingerprint("merge:premergecommit", o),
	}
	if err := appendEscape(closed); err != nil {
		t.Fatal(err)
	}
	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); !recorded {
		t.Fatal("a hole that reopens after a fix is news, not a duplicate")
	}
}

// --- trigger (a): the merge gate refusing a lane the commit gate passed -----

// makeMergeInProgress commits a lane tip on a branch and puts the process in
// the state a pre-merge-commit hook sees.
func makeMergeInProgress(t *testing.T, root, message string) {
	t.Helper()
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "lane.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", message)
	gitDo(t, root, "checkout", "-q", "-")
	t.Setenv(reflogActionEnv, "merge lane")
}

// Two gates disagreeing about one tree is the gate's own evidence that the
// cheaper one is missing a stage. Nothing recorded it before.
func TestMergeGateRefusingALaneTheCommitGatePassedRecordsAnEscape(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// No PATH surgery: git has to stay reachable. The fixture repo has no
	// GitHub remote, which is what keeps the recorder from opening an issue.
	root := makeGoRepo(t)
	makeMergeInProgress(t, root, "Land the lane")
	// The trailer has to name the tip's OWN tree, which only exists once the
	// commit does.
	retrailerLaneTip(t, root)

	NoteMergeGateEscape(root, "gate premergecommit: clippy failed on the combined tree", io.Discard)

	recs := readEscapes(t)
	if len(recs) != 1 {
		t.Fatalf("recorded %d escapes, want 1", len(recs))
	}
	if recs[0].Kind != EscapeKind {
		t.Errorf("kind = %q, want %q", recs[0].Kind, EscapeKind)
	}
	if !strings.Contains(recs[0].Reason, "pre-commit") {
		t.Errorf("the reason must say the commit gate passed the same tree: %q", recs[0].Reason)
	}
}

// A lane the commit gate never passed being refused at merge is the gate
// WORKING. Recording it would bury the real evidence in noise.
func TestMergeGateRefusingALaneWithNoGreenGateRecordsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// No PATH surgery: git has to stay reachable. The fixture repo has no
	// GitHub remote, which is what keeps the recorder from opening an issue.
	root := makeGoRepo(t)
	makeMergeInProgress(t, root, "Land the lane")

	NoteMergeGateEscape(root, "gate premergecommit: clippy failed on the combined tree", io.Discard)

	if n := len(readEscapes(t)); n != 0 {
		t.Fatalf("recorded %d escapes, want 0 — the merge gate caught what precommit never judged", n)
	}
}

// --- trigger (c): unaccepted survivors at merge -----------------------------

// A survivor at the merge is a code path no test constrains, arriving at the
// last gate that could stop it — an escape whether or not the commit gate
// ever passed the tree, because the commit gate does not judge survivors.
func TestUnacceptedSurvivorsAtMergeRecordAnEscape(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// No PATH surgery: git has to stay reachable. The fixture repo has no
	// GitHub remote, which is what keeps the recorder from opening an issue.
	root := makeGoRepo(t)
	makeMergeInProgress(t, root, "Land the lane")

	NoteMergeGateEscape(root, unacceptedSurvivorRejectionSample(), io.Discard)

	recs := readEscapes(t)
	if len(recs) != 1 {
		t.Fatalf("recorded %d escapes, want 1", len(recs))
	}
	if !strings.Contains(recs[0].Reason, "survivor") {
		t.Errorf("the reason must name the survivors: %q", recs[0].Reason)
	}
}

// The predicate reads the receipt gate's OWN rejection. If that sentence is
// reworded and the predicate is not, the trigger goes quietly dead.
func TestTheSurvivorPredicateMatchesTheReceiptGatesOwnRejection(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	res := blockReceipt("%d unaccepted survivor(s), starting with %s — a code path no test constrains", 2, "x.rs:1")
	if !isUnacceptedSurvivorRejection(res.Message) {
		t.Fatalf("the predicate must recognise the receipt gate's rejection:\n%s", res.Message)
	}
	other := blockReceipt("worktree_dirty: the run measured uncommitted work, not what is being merged")
	if isUnacceptedSurvivorRejection(other.Message) {
		t.Fatalf("a different receipt rejection is not a survivor escape:\n%s", other.Message)
	}
}

// --- trigger (b): CI red on a tip the local gate passed green ---------------

// CI failing on a tip the local gate passed IS the definition of an escape:
// the two ran on the same tree and disagreed.
func TestCIFailureOnAGreenTipRecordsAnEscape(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// No PATH surgery: git has to stay reachable. The fixture repo has no
	// GitHub remote, which is what keeps the recorder from opening an issue.
	root := makeGoRepo(t)
	commitWithGreenGateTrailer(t, root, "Land it")

	r, recorded := RecordCIEscape(root, "build (ubuntu-latest)", "clippy: unused variable", io.Discard)
	if !recorded {
		t.Fatal("CI red on a locally-green tip must be recorded")
	}
	if r.FromCI != "build (ubuntu-latest)" {
		t.Errorf("the record must name the job: %+v", r)
	}
}

// A tip the local gate never passed failing in CI says nothing about the
// gate — it says somebody pushed without running it.
func TestCIFailureOnATipWithNoGreenGateRecordsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// No PATH surgery: git has to stay reachable. The fixture repo has no
	// GitHub remote, which is what keeps the recorder from opening an issue.
	root := makeGoRepo(t)

	if _, recorded := RecordCIEscape(root, "build", "boom", io.Discard); recorded {
		t.Fatal("a tip with no green gate carries no claim for CI to contradict")
	}
	if n := len(readEscapes(t)); n != 0 {
		t.Fatalf("recorded %d escapes, want 0", n)
	}
}

// --- trigger (d): the overrides, as false-positive candidates ---------------

// A check refused an edit and the same edit went in on a waiver moments
// later. That is the check being talked past, which is exactly the shape a
// false positive has.
func TestOverrideCandidatesNameADeniedEditThatWentThroughOnAWaiver(t *testing.T) {
	now := time.Now().UTC()
	log := gateLines(now,
		"preedit root a_test.go pretooluse-denied:test-sleep",
		"preedit root a_test.go smell-escape:test-sleep",
	)
	got := OverrideCandidates(strings.NewReader(log), now)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Reason, "test-sleep") {
		t.Errorf("the candidate must name the check: %q", got[0].Reason)
	}
}

// A denial nobody talked past is the check WORKING.
func TestADenialWithNoOverrideIsNotACandidate(t *testing.T) {
	now := time.Now().UTC()
	log := gateLines(now, "preedit root a_test.go pretooluse-denied:test-sleep")
	if got := OverrideCandidates(strings.NewReader(log), now); len(got) != 0 {
		t.Fatalf("got %d candidates, want 0: %+v", len(got), got)
	}
}

// Turning the gate off is the loudest false-positive signal there is: the
// session decided the whole gate was in its way.
func TestOverrideCandidatesNameASessionThatTurnedTheGateOff(t *testing.T) {
	now := time.Now().UTC()
	log := gateLines(now, "session root s1 override-off")
	got := OverrideCandidates(strings.NewReader(log), now)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Stage, "override-off") {
		t.Errorf("the candidate must name the override: %+v", got[0])
	}
}

// Old noise is not a signal. The window is what keeps the sync from
// re-opening last quarter's arguments.
func TestOverrideCandidatesIgnoreWhatIsOlderThanTheWindow(t *testing.T) {
	now := time.Now().UTC()
	log := gateLines(now.Add(-escapeDedupeWindow-time.Hour), "session root s1 override-off")
	if got := OverrideCandidates(strings.NewReader(log), now); len(got) != 0 {
		t.Fatalf("got %d candidates, want 0: %+v", len(got), got)
	}
}

// gateLines renders gate.log lines at ts, in the exact shape appendGateLog
// writes: the parser reads by field position, so a hand-built line that
// drifts from the writer would test the wrong thing.
func gateLines(ts time.Time, entries ...string) string {
	var b strings.Builder
	for i, e := range entries {
		b.WriteString(ts.Add(time.Duration(i) * time.Minute).Format(time.RFC3339))
		b.WriteString(" " + e + " 0.0s\n")
	}
	return b.String()
}

// commitWithGreenGateTrailer commits a tree the gate has stamped green, with
// the trailer the commit-msg hook would have written.
func commitWithGreenGateTrailer(t *testing.T, root, subject string) {
	t.Helper()
	write(t, root, "landed.go", "package m\n")
	gitDo(t, root, "add", ".")
	tree := gitOutT(t, root, "write-tree")
	gitDo(t, root, "commit", "-q", "-m", subject+"\n\n"+gateGreenTrailer(tree))
}

// retrailerLaneTip rewrites the lane tip's message so its trailer names the
// tip's own tree — which only exists once the commit does.
func retrailerLaneTip(t *testing.T, root string) {
	t.Helper()
	tree := gitOutT(t, root, "rev-parse", "lane:")
	msg := gitOutT(t, root, "log", "-1", "--format=%s", "lane")
	gitDo(t, root, "checkout", "-q", "lane")
	gitDo(t, root, "commit", "-q", "--amend", "-m", msg+"\n\n"+gateGreenTrailer(tree))
	// Amending changed the commit but not the tree, so the trailer still
	// names what the tip holds.
	if got := gitOutT(t, root, "rev-parse", "lane:"); got != tree {
		t.Fatalf("amending changed the tree: %s vs %s", got, tree)
	}
	gitDo(t, root, "checkout", "-q", "-")
}

// unacceptedSurvivorRejectionSample is the receipt gate's own words, so this
// fixture cannot drift from the sentence the trigger reads.
func unacceptedSurvivorRejectionSample() string {
	return blockReceipt("%d unaccepted survivor(s), starting with %s — a code path no test constrains",
		1, "src/lib.rs:12").Message
}

// A waiver on a file no check ever refused is an author declaring an
// exception, not a check being talked past — and the pairing is the only
// thing that tells the two apart.
func TestAWaiverWithNoPrecedingDenialIsNotACandidate(t *testing.T) {
	now := time.Now().UTC()
	log := gateLines(now, "preedit root a_test.go smell-escape:test-sleep")
	if got := OverrideCandidates(strings.NewReader(log), now); len(got) != 0 {
		t.Fatalf("got %d candidates, want 0: %+v", len(got), got)
	}
}
