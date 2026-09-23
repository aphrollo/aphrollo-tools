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

// --- the channel: a git note on the commit the gate passed ------------------

// gitOutT is a git value a test needs to compare against.
func gitOutT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// gitNote is the gate note on rev, "" when there is none.
func gitNote(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command(gitBinary(), "notes", "--ref="+gateNotesRef, "show", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CI runs on a box that has never seen this machine's gate state, so the only
// thing that can tell it "the local gate ran a suite and it passed on this
// exact tree" is something carried WITH the commit. The note is that channel.
func TestPostCommitWritesTheGateNoteForATreeWhoseSuiteWentGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampProvenSuite(root)
	gitDo(t, root, "commit", "-q", "-m", "Add the next thing")

	PostCommit(root)

	tree := gitOutT(t, root, "rev-parse", "HEAD:")
	if got := gitNote(t, root, "HEAD"); got != gateGreenNote(tree) {
		t.Fatalf("note = %q, want %q", got, gateGreenNote(tree))
	}
}

// A commit whose suite never ran green must not claim it did — that claim is
// the whole basis on which CI later calls a failure an escape.
func TestPostCommitWritesNoNoteForATreeNoSuiteProved(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "Add the next thing")

	PostCommit(root)

	if got := gitNote(t, root, "HEAD"); got != "" {
		t.Fatalf("an unproven tree must carry no note, got %q", got)
	}
}

// An amend makes a NEW commit that no suite has run against — the pre-commit
// gate's cache answers "unchanged", which is not the same as "a suite passed
// here". No note is the correct answer, and it is what the consumed stamp
// produces.
func TestAnAmendedCommitCarriesNoGateNote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "next.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampProvenSuite(root)
	gitDo(t, root, "commit", "-q", "-m", "Add the next thing")
	PostCommit(root)

	gitDo(t, root, "commit", "-q", "--amend", "-m", "Add the next thing, better said")
	PostCommit(root)

	if got := gitNote(t, root, "HEAD"); got != "" {
		t.Fatalf("an amended commit carries no note, got %q", got)
	}
}

// The stamp is per TREE: one left by an earlier commit must not vouch for the
// one being written now.
func TestPostCommitWritesNoNoteForADifferentTreeThanTheOneStamped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampProvenSuite(root)
	// Stage something else: the tree a suite proved is no longer the tree
	// about to be committed.
	write(t, root, "b.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "Add b")

	PostCommit(root)

	if got := gitNote(t, root, "HEAD"); got != "" {
		t.Fatalf("a stamp for another tree must not vouch for this one, got %q", got)
	}
}

// `git commit -a` hands the hook a TEMPORARY index through GIT_INDEX_FILE.
// Reading .git/index instead names the wrong tree — and, while the commit
// holds index.lock, can fail outright — so the stamp silently never matches
// and no commit made that way is ever gated in CI's eyes.
func TestGreenSuiteIsStampedUnderCommitAll(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	// Tracked and committed first, so `commit -a` has something to pick up.
	write(t, root, "tracked.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "base two")
	write(t, root, "tracked.go", "package m\n\n// changed\n")

	// What the pre-commit hook sees under `git commit -a`: git has already
	// built the temporary index the commit will use.
	tmpIndex := filepath.Join(t.TempDir(), "tmp-index")
	gitEnvDo(t, root, []string{"GIT_INDEX_FILE=" + tmpIndex}, "read-tree", "HEAD")
	gitEnvDo(t, root, []string{"GIT_INDEX_FILE=" + tmpIndex}, "add", "-u")
	t.Setenv("GIT_INDEX_FILE", tmpIndex)
	want := strings.TrimSpace(gitEnvOut(t, root, []string{"GIT_INDEX_FILE=" + tmpIndex}, "write-tree"))

	stampGreenSuite(root)

	got := readGreenSuiteStamp(t, root)
	if got != want {
		t.Fatalf("stamped tree %q, want the temporary index's tree %q", got, want)
	}
}

// A note names the tree it was written for, so one copied onto another commit
// describes content that commit does not have. It is not tamper-proof — a
// note can be rewritten — but it does not travel between trees by accident,
// which is what a rebase or a cherry-pick would otherwise do to it.
func TestANoteTransplantedOntoAnotherCommitIsIgnored(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampProvenSuite(root)
	gitDo(t, root, "commit", "-q", "-m", "Proven")
	PostCommit(root)
	proven := gitOutT(t, root, "rev-parse", "HEAD")
	note := gitNote(t, root, "HEAD")

	// A second commit nothing proved, wearing the first one's note.
	write(t, root, "b.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "Unproven")
	gitDo(t, root, "notes", "--ref="+gateNotesRef, "add", "-f", "-m", note, "HEAD")

	if !commitCarriesGreenGate(root, proven) {
		t.Fatal("the commit the note was written for must still be believed")
	}
	if commitCarriesGreenGate(root, "HEAD") {
		t.Fatal("a note naming another tree must be ignored")
	}
}

// gitEnvDo runs git with extra environment entries.
func gitEnvDo(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	if out := gitEnvRun(t, dir, env, args...); out == "" {
		return
	}
}

func gitEnvOut(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	return gitEnvRun(t, dir, env, args...)
}

func gitEnvRun(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return string(out)
}

// readGreenSuiteStamp reads the tree the gate stamped for root.
func readGreenSuiteStamp(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(greenSuiteStampFile(root))
	if err != nil {
		t.Fatalf("no green-suite stamp: %v", err)
	}
	return strings.TrimSpace(string(data))
}

// --- the fingerprint dedupe -------------------------------------------------

// The same failing stage repeating every merge must be ONE issue. Without a
// dedupe the automatic recorder turns a recurring red into a stream of
// identical issues, which is worse than recording nothing.
func TestASecondIdenticalEscapeWithinTheWindowRecordsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGh(t, "https://github.com/o/r/issues/4")
	o := EscapeOptions{Reason: "the merge gate refused a green lane", Evidence: "clippy: unused variable", Repo: repo}

	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); !recorded {
		t.Fatal("the first sighting must be recorded")
	}
	if _, recorded := recordEscapeOnce("merge:premergecommit", o, io.Discard); recorded {
		t.Fatal("the same stage and diagnostic inside the window must record nothing")
	}
	if n := len(readEscapes(t)); n != 1 {
		t.Fatalf("escapes.jsonl holds %d records, want 1", n)
	}
	if n := strings.Count(ghArgv(t, log), "issue create"); n != 1 {
		t.Fatalf("gh ran `issue create` %d times, want 1:\n%s", n, ghArgv(t, log))
	}
}

// Seven days on, the same class escaping AGAIN is news: it means the first
// issue never closed the hole.
func TestTheSameEscapeAfterTheWindowIsRecordedAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	o := EscapeOptions{Reason: "the merge gate refused a green lane", Evidence: "clippy: unused variable"}
	stale := EscapeRecord{
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
	// The note names the tip's OWN tree, which only exists once the commit
	// does, so it is written after the fact.
	noteLaneTipGreen(t, root)

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

// The predicate reads the mutation stage's OWN refusal, built here by the
// judge that writes it. If that sentence is reworded and the predicate is
// not, the trigger goes quietly dead.
func TestTheSurvivorPredicate_MatchesTheMutationStagesOwnRefusal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	refused := judgeMutants(MutantsConfig{}, []MutantOutcome{
		{File: "x.rs", Line: 1, Col: 3, Mutation: "replace + with -", Status: "missed"},
	})
	if !isUnacceptedSurvivorRejection(refused.Message) {
		t.Fatalf("the predicate must recognise the stage's own refusal:\n%s", refused.Message)
	}
	passed := judgeMutants(MutantsConfig{}, []MutantOutcome{
		{File: "x.rs", Line: 1, Col: 3, Mutation: "replace + with -", Status: "caught"},
	})
	if isUnacceptedSurvivorRejection(passed.Message) {
		t.Fatalf("a clean measurement is not a survivor escape:\n%s", passed.Message)
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
	commitWithGreenGateNote(t, root, "Land it")

	r, recorded := RecordCIEscape(CIEscapeOptions{Repo: root, Job: "build (ubuntu-latest)", Evidence: "clippy: unused variable"}, io.Discard)
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

	if _, recorded := RecordCIEscape(CIEscapeOptions{Repo: root, Job: "build", Evidence: "boom"}, io.Discard); recorded {
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

// commitWithGreenGateNote commits a tree a suite proved green, carrying the
// note the post-commit hook would have written.
func commitWithGreenGateNote(t *testing.T, root, subject string) {
	t.Helper()
	write(t, root, "landed.go", "package m\n")
	gitDo(t, root, "add", ".")
	stampProvenSuite(root)
	gitDo(t, root, "commit", "-q", "-m", subject)
	PostCommit(root)
}

// noteLaneTipGreen puts onto the lane tip the note it would carry after a
// green local gate. It names the tip's OWN tree, which only exists once the
// commit does.
func noteLaneTipGreen(t *testing.T, root string) {
	t.Helper()
	tree := gitOutT(t, root, "rev-parse", "lane:")
	gitDo(t, root, "notes", "--ref="+gateNotesRef, "add", "-f", "-m", gateGreenNote(tree), "lane")
}

// unacceptedSurvivorRejectionSample is the mutation stage's own words, built
// by the judge that writes them, so this fixture cannot drift from the
// sentence the trigger reads.
func unacceptedSurvivorRejectionSample() string {
	return judgeMutants(MutantsConfig{}, []MutantOutcome{
		{File: "src/lib.rs", Line: 12, Col: 5, Mutation: "replace + with -", Status: "missed"},
	}).Message
}
