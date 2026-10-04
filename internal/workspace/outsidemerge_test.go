package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A merge made outside `workspace merge` (the GitHub web UI, `gh pr merge`, a
// terminal) leaves the verb's merge record unwritten, so the event log never
// sees it. When the local trunk takes it in, it is recorded once as a merge
// event by=outside and as an escape of verdict outside-merge. These drive real
// repositories with a local bare origin; no gh, no network.

// originCheckout is a fresh clone of the clone's origin with an identity, the
// working copy a GitHub-side merge is made in.
func originCheckout(t *testing.T, clone string) string {
	t.Helper()
	url, err := exec.Command("git", "-C", clone, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	gitRun(t, tmp, "clone", "-q", strings.TrimSpace(string(url)), tmp)
	gitRun(t, tmp, "config", "user.email", "t@t")
	gitRun(t, tmp, "config", "user.name", "t")
	return tmp
}

// uniqueFile names a file after its commit subject so no two landings collide.
func uniqueFile(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return "f-" + hex.EncodeToString(sum[:4]) + ".txt"
}

// landOnOrigin pushes one commit with the given subject onto origin's main, as
// a GitHub squash merge lands one, and returns its sha.
func landOnOrigin(t *testing.T, clone, subject string) string {
	t.Helper()
	tmp := originCheckout(t, clone)
	writeFile(t, tmp, uniqueFile(subject), subject+"\n")
	gitRun(t, tmp, "add", ".")
	gitRun(t, tmp, "commit", "-q", "-m", subject)
	gitRun(t, tmp, "push", "-q", "origin", "main")
	return revOf(t, tmp, "HEAD")
}

// landMergeOnOrigin pushes a --no-ff merge of a one-commit branch onto origin's
// main, the shape of a merge made by hand or GitHub's "Create a merge commit",
// and returns the merge commit's sha.
func landMergeOnOrigin(t *testing.T, clone, subject string) string {
	t.Helper()
	tmp := originCheckout(t, clone)
	gitRun(t, tmp, "switch", "-q", "-c", "topic")
	writeFile(t, tmp, uniqueFile(subject), subject+"\n")
	gitRun(t, tmp, "add", ".")
	gitRun(t, tmp, "commit", "-q", "-m", "topic work")
	gitRun(t, tmp, "switch", "-q", "main")
	gitRun(t, tmp, "merge", "-q", "--no-ff", "-m", subject, "topic")
	gitRun(t, tmp, "push", "-q", "origin", "main")
	return revOf(t, tmp, "HEAD")
}

// outsideMerges are the merge events recorded as made outside the verb.
func outsideMerges(t *testing.T) []tdd.Event {
	t.Helper()
	var out []tdd.Event
	for _, e := range ofKind(emitted(t), "merge") {
		if e.Detail["by"] == "outside" {
			out = append(out, e)
		}
	}
	return out
}

func TestSync_RecordsAnOutsideMergeOncePerCommit(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	first := landOnOrigin(t, clone, "Add the frobnicator (#1089)")
	if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	second := landOnOrigin(t, clone, "Mend the widget (#1090)")
	for range 2 { // the second pass has nothing to move and must record nothing
		if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	merges := outsideMerges(t)
	if len(merges) != 2 {
		t.Fatalf("outside merge events = %+v, want exactly two", merges)
	}
	for i, want := range []struct{ sha, pr string }{{first, "1089"}, {second, "1090"}} {
		e := merges[i]
		if e.Detail["sha"] != want.sha || e.Detail["pr"] != want.pr || e.Verdict != "ok" {
			t.Errorf("merge event %d = %+v, want ok for sha %s PR %s", i, e, want.sha, want.pr)
		}
	}
	escapes := ofKind(emitted(t), "escape")
	if len(escapes) != 2 {
		t.Fatalf("escape events = %+v, want one per outside merge", escapes)
	}
	if escapes[0].Verdict != "outside-merge" || escapes[0].Detail["sha"] != first || escapes[0].Detail["pr"] != "1089" {
		t.Errorf("first escape = %+v, want verdict outside-merge for %s PR 1089", escapes[0], first)
	}
}

// The event is stamped with the commit's own time, so a backfill lands in the
// week the merge happened rather than the week it was found.
func TestSync_StampsAnOutsideMergeWithTheCommitTime(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	tmp := originCheckout(t, clone)
	writeFile(t, tmp, "late.txt", "late\n")
	gitRun(t, tmp, "add", ".")
	cmd := exec.Command("git", "-C", tmp, "commit", "-q", "-m", "Old merge (#7)")
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2026-03-04T05:06:07Z", "GIT_AUTHOR_DATE=2026-03-04T05:06:07Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	gitRun(t, tmp, "push", "-q", "origin", "main")

	if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	merges := outsideMerges(t)
	if len(merges) != 1 || merges[0].At != "2026-03-04T05:06:07Z" {
		t.Fatalf("outside merge events = %+v, want one stamped 2026-03-04T05:06:07Z", merges)
	}
}

// A merge commit made without a PR (a hand `git merge --no-ff`) is a merge too;
// it carries no PR number. A plain commit pushed straight to trunk is not one.
func TestSync_RecordsAMergeCommitWithNoPRAndIgnoresAPlainCommit(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	landOnOrigin(t, clone, "Direct commit to trunk")
	merge := landMergeOnOrigin(t, clone, "merge topic by hand")

	var out bytes.Buffer
	if err := Sync(clone, false, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	merges := outsideMerges(t)
	if len(merges) != 1 || merges[0].Detail["sha"] != merge {
		t.Fatalf("outside merge events = %+v, want only the merge commit %s", merges, merge)
	}
	if got, ok := merges[0].Detail["pr"]; ok {
		t.Errorf("pr = %q on a merge whose subject names none", got)
	}
	if !strings.Contains(out.String(), merge[:7]) || strings.Contains(out.String(), "#0") {
		t.Errorf("sync output = %q, want the merge named by its short sha %s, not as a PR", out.String(), merge[:7])
	}
}

// A run that died between the two writes left the merge event and no escape;
// the next run finishes the pair instead of skipping the commit or doubling the
// merge.
func TestSync_FinishesAPairAnEarlierRunLeftHalfWritten(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	sha := landOnOrigin(t, clone, "Add the frobnicator (#1089)")
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Verdict: "ok",
		Detail: map[string]string{"sha": sha, "by": "outside", "pr": "1089"}})

	var out bytes.Buffer
	if err := Sync(clone, false, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if merges := outsideMerges(t); len(merges) != 1 {
		t.Errorf("outside merge events = %+v, want the one already written", merges)
	}
	if escapes := ofKind(emitted(t), "escape"); len(escapes) != 1 || escapes[0].Detail["sha"] != sha {
		t.Errorf("escape events = %+v, want the missing one for %s", escapes, sha)
	}
	if !strings.Contains(out.String(), "recorded 1 merge(s)") {
		t.Errorf("sync output = %q, want the finished commit reported", out.String())
	}
}

func TestSync_ReadsThePRFromGitHubsMergeCommitSubject(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	landMergeOnOrigin(t, clone, "Merge pull request #12 from o/topic")

	if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	merges := outsideMerges(t)
	if len(merges) != 1 || merges[0].Detail["pr"] != "12" {
		t.Fatalf("outside merge events = %+v, want one for PR 12", merges)
	}
}

func TestPRNumber_ReadsTheSubjectsAMergeCarries(t *testing.T) {
	for _, c := range []struct {
		subject string
		want    int
	}{
		{"Add the frobnicator (#1089)", 1089},
		{"Merge pull request #12 from o/topic", 12},
		{"Fix #12 in the parser", 0},
		{"Rework (#4) for (#9)", 9},
		{"Close the loop (#5) now", 0},
		{"Past any int (#99999999999999999999)", 0},
		{"", 0},
	} {
		if got := prNumber(c.subject); got != c.want {
			t.Errorf("prNumber(%q) = %d, want %d", c.subject, got, c.want)
		}
	}
}

// The verb records its merge before it moves local trunk, so the trunk move
// finds the record and writes nothing of its own: the verb's merges never count
// as outside ones.
func TestMergeApply_OwnMergeIsNotRecordedAsOutside(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)

	verbMergesPR18(t, clone, targetFor(clone, "lane/z"))
}

// The verb really runs in a lane worktree, and its record then names the repo
// through that worktree's link; the trunk move must still recognise it.
func TestMergeApply_OwnMergeFromALaneWorktreeIsNotRecordedAsOutside(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	lane := filepath.Join(t.TempDir(), "lane-z")
	gitRun(t, clone, "worktree", "add", "-q", "-b", "lane/z", lane)

	verbMergesPR18(t, clone, &Target{Worktree: lane, Branch: "lane/z", MainRepo: clone, RepoName: filepath.Base(clone)})
}

// verbMergesPR18 drives `workspace merge` of PR 18 with GitHub stubbed to land
// one squash commit on origin, then holds that the verb's trunk move recorded
// nothing as outside and left only the verb's own merge event.
func verbMergesPR18(t *testing.T, clone string, tgt *Target) {
	t.Helper()
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 18, URL: "u", State: "OPEN", HeadSHA: "abc123"}, nil
		},
		func(wt, branch, method, sha string) error { landOnOrigin(t, clone, "Ship it (#18)"); return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	stubPremergeGate(t, func(tgt *Target, _ string, _ *tdd.CIVerdict, log io.Writer) error { return nil })
	stubRetro(t, new([]string))
	// syncMainClone stays the real Sync: it is what moves local trunk.

	m, _ := MergePlan(tgt, "squash", true)
	var out bytes.Buffer
	if err := m.Apply(&out, &bytes.Buffer{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if strings.Contains(out.String(), "made outside") {
		t.Errorf("verb output = %q, want no outside-merge line for its own merge", out.String())
	}
	if revOf(t, clone, "refs/heads/main") != revOf(t, clone, "refs/remotes/origin/main") {
		t.Fatal("the verb's sync did not move local trunk, so this proves nothing")
	}
	if merges := ofKind(emitted(t), "merge"); len(merges) != 1 || merges[0].Detail["by"] == "outside" || merges[0].Detail["pr"] != "18" {
		t.Fatalf("merge events = %+v, want only the verb's own, for PR 18", merges)
	}
	if escapes := ofKind(emitted(t), "escape"); len(escapes) != 0 {
		t.Fatalf("escape events = %+v, want none for the verb's own merge", escapes)
	}
}

// PR numbers repeat across repositories, so the verb's record of PR 18 in one
// repository says nothing about a merge of PR 18 in another.
func TestSync_AVerbRecordOfTheSamePRInAnotherRepoDoesNotHideAnOutsideMerge(t *testing.T) {
	gateState(t)
	elsewhere := t.TempDir()
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: elsewhere, Verdict: "ok",
		Detail: map[string]string{"pr": "18", "method": "squash"}})
	clone := repoWithOrigin(t)
	landOnOrigin(t, clone, "Ship it (#18)")

	if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if merges := outsideMerges(t); len(merges) != 1 || merges[0].Detail["pr"] != "18" {
		t.Fatalf("outside merge events = %+v, want PR 18 recorded for this repo", merges)
	}
}

func TestSync_DryRunRecordsNothing(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	landOnOrigin(t, clone, "Add the frobnicator (#1089)")

	if err := Sync(clone, true, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if evs := emitted(t); len(evs) != 0 {
		t.Fatalf("events = %+v, want none from a dry run", evs)
	}
}

// gatedClone is a clone that carries an aphrollo.toml, the repo's declaration
// that it uses this tool; the machine-wide post-merge hook records only there.
func gatedClone(t *testing.T) string {
	t.Helper()
	clone := repoWithOrigin(t)
	writeFile(t, clone, "aphrollo.toml", "[aphrollo]\n")
	return clone
}

// pullFastForward moves the clone's checked-out branch to origin/main the way
// `git pull` does, which sets ORIG_HEAD before the post-merge hook would run.
func pullFastForward(t *testing.T, clone string) {
	t.Helper()
	gitRun(t, clone, "fetch", "-q", "origin")
	gitRun(t, clone, "merge", "-q", "--ff-only", "origin/main")
}

func TestPostMergeRecord_RecordsWhatThePullBroughtInOnceAndSaysSo(t *testing.T) {
	gateState(t)
	clone := gatedClone(t)
	sha := landOnOrigin(t, clone, "Add the frobnicator (#1089)")
	pullFastForward(t, clone)

	var errb bytes.Buffer
	PostMergeRecord(clone, &errb)
	PostMergeRecord(clone, &errb) // the same ORIG_HEAD range again

	merges := outsideMerges(t)
	if len(merges) != 1 || merges[0].Detail["sha"] != sha || merges[0].Detail["pr"] != "1089" {
		t.Fatalf("outside merge events = %+v, want one for %s PR 1089", merges, sha)
	}
	if escapes := ofKind(emitted(t), "escape"); len(escapes) != 1 {
		t.Fatalf("escape events = %+v, want one", escapes)
	}
	if got := errb.String(); strings.Count(got, "#1089") != 1 {
		t.Errorf("hook output = %q, want one line naming #1089", got)
	}
}

// Only what the merge that fired the hook brought in is looked at: a merge
// already on trunk before ORIG_HEAD is the backfill's business, not the hook's.
func TestPostMergeRecord_LooksOnlyAtWhatTheLastMergeBroughtIn(t *testing.T) {
	gateState(t)
	clone := gatedClone(t)
	landOnOrigin(t, clone, "Older (#1)")
	pullFastForward(t, clone) // no hook ran for this one
	landOnOrigin(t, clone, "Newer (#2)")
	pullFastForward(t, clone)

	PostMergeRecord(clone, &bytes.Buffer{})

	if merges := outsideMerges(t); len(merges) != 1 || merges[0].Detail["pr"] != "2" {
		t.Fatalf("outside merge events = %+v, want only PR 2", merges)
	}
}

// Merging trunk into a lane fires the same hook; the lane is not trunk, so
// nothing the merge brought in landed on trunk just now.
func TestPostMergeRecord_IgnoresAMergeIntoALane(t *testing.T) {
	gateState(t)
	clone := gatedClone(t)
	gitRun(t, clone, "switch", "-q", "-c", "lane/x")
	landOnOrigin(t, clone, "Add the frobnicator (#1089)")
	gitRun(t, clone, "fetch", "-q", "origin")
	gitRun(t, clone, "merge", "-q", "--no-edit", "origin/main")

	PostMergeRecord(clone, &bytes.Buffer{})

	if evs := emitted(t); len(evs) != 0 {
		t.Fatalf("events = %+v, want none for a merge into a lane", evs)
	}
}

// core.hooksPath is machine-wide, so the hook fires in every repo on the box;
// a repo that never declared itself aphrollo's is left alone.
func TestPostMergeRecord_LeavesARepoWithoutAnAphrolloFootprintAlone(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	landOnOrigin(t, clone, "Add the frobnicator (#1089)")
	pullFastForward(t, clone)

	var errb bytes.Buffer
	PostMergeRecord(clone, &errb)

	if evs := emitted(t); len(evs) != 0 || errb.Len() != 0 {
		t.Fatalf("events = %+v, output = %q, want both empty in an undeclared repo", evs, errb.String())
	}
}

func TestPostMergeRecord_RecognisesEachWayARepoDeclaresItself(t *testing.T) {
	for _, c := range []struct {
		name    string
		declare func(t *testing.T, clone string)
		want    int
	}{
		{"aphrollo.toml", func(t *testing.T, clone string) { writeFile(t, clone, "aphrollo.toml", "[aphrollo]\n") }, 1},
		{".ratchet directory", func(t *testing.T, clone string) {
			if err := os.Mkdir(filepath.Join(clone, ".ratchet"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, 1},
		{"Cargo aphrollo table", func(t *testing.T, clone string) {
			writeFile(t, clone, "Cargo.toml", "[workspace]\nmembers = []\n\n[workspace.metadata.aphrollo]\nundercover = true\n")
		}, 1},
		{"Cargo.toml without the table", func(t *testing.T, clone string) {
			writeFile(t, clone, "Cargo.toml", "[workspace]\nmembers = []\n")
		}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			gateState(t)
			clone := repoWithOrigin(t)
			c.declare(t, clone)
			landOnOrigin(t, clone, "Add the frobnicator (#1089)")
			pullFastForward(t, clone)

			PostMergeRecord(clone, &bytes.Buffer{})

			if got := len(outsideMerges(t)); got != c.want {
				t.Fatalf("outside merge events = %d, want %d", got, c.want)
			}
		})
	}
}

func TestSameDir_RecognisesOneDirectoryUnderTwoSpellings(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	t.Chdir(dir)
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{dir, dir, true},
		{".", dir, true},
		{dir, other, false},
		{"", dir, false},
		{filepath.Join(dir, "missing"), dir, false},
	} {
		if got := sameDir(c.a, c.b); got != c.want {
			t.Errorf("sameDir(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestPostMergeRecord_IsSilentOutsideARepo(t *testing.T) {
	gateState(t)
	var errb bytes.Buffer
	PostMergeRecord(t.TempDir(), &errb)
	if evs := emitted(t); len(evs) != 0 || errb.Len() != 0 {
		t.Fatalf("events = %+v, output = %q, want both empty outside a repo", evs, errb.String())
	}
}

// SyncSince is the one-time backfill: it records the merges in <ref>..trunk the
// log never saw, previews them with --dry, and never records a sha twice.
func TestSyncSince_BackfillsARangeOnceAndDryRunWritesNothing(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	seed := revOf(t, clone, "HEAD")
	landOnOrigin(t, clone, "First (#1089)")
	landOnOrigin(t, clone, "Second (#1090)")
	pullFastForward(t, clone) // trunk is current, so the sync's own move records nothing

	var dry bytes.Buffer
	if err := SyncSince(clone, seed, true, &dry, &bytes.Buffer{}); err != nil {
		t.Fatalf("SyncSince --dry: %v", err)
	}
	if evs := emitted(t); len(evs) != 0 {
		t.Fatalf("events = %+v, want none from --dry", evs)
	}
	if !strings.Contains(dry.String(), "#1089") || !strings.Contains(dry.String(), "#1090") {
		t.Errorf("--dry output = %q, want both PRs named", dry.String())
	}

	var first, again bytes.Buffer
	if err := SyncSince(clone, seed, false, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("SyncSince: %v", err)
	}
	if err := SyncSince(clone, seed, false, &again, &bytes.Buffer{}); err != nil {
		t.Fatalf("SyncSince: %v", err)
	}
	if !strings.Contains(first.String(), "recorded 2 merge(s)") {
		t.Errorf("first run output = %q, want two merges recorded", first.String())
	}
	if !strings.Contains(again.String(), "[skip]") || strings.Contains(again.String(), "recorded") {
		t.Errorf("second run output = %q, want a skip and nothing recorded", again.String())
	}
	merges := outsideMerges(t)
	if len(merges) != 2 || merges[0].Detail["pr"] != "1089" || merges[1].Detail["pr"] != "1090" {
		t.Fatalf("outside merge events = %+v, want PRs 1089 then 1090, once each", merges)
	}
	if escapes := ofKind(emitted(t), "escape"); len(escapes) != 2 {
		t.Fatalf("escape events = %+v, want two", escapes)
	}
}

// A merge the verb recorded stays out of the backfill's range too.
func TestSyncSince_SkipsAMergeTheVerbRecorded(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	seed := revOf(t, clone, "HEAD")
	landOnOrigin(t, clone, "By the verb (#1091)")
	landOnOrigin(t, clone, "By hand (#1092)")
	pullFastForward(t, clone)
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Verdict: "ok",
		Detail: map[string]string{"pr": strconv.Itoa(1091), "method": "squash"}})

	if err := SyncSince(clone, seed, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("SyncSince: %v", err)
	}

	if merges := outsideMerges(t); len(merges) != 1 || merges[0].Detail["pr"] != "1092" {
		t.Fatalf("outside merge events = %+v, want only PR 1092", merges)
	}
}

// A repository with no remote has no origin tip to scan to; the backfill reads
// local trunk instead.
func TestSyncSince_ReadsLocalTrunkWhenThereIsNoRemote(t *testing.T) {
	gateState(t)
	repo := initRepo(t)
	seed := revOf(t, repo, "HEAD")
	gitRun(t, repo, "switch", "-q", "-c", "topic")
	writeFile(t, repo, "topic.txt", "topic\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "topic work")
	gitRun(t, repo, "switch", "-q", "main")
	gitRun(t, repo, "merge", "-q", "--no-ff", "-m", "merge topic by hand", "topic")
	merge := revOf(t, repo, "HEAD")

	if err := SyncSince(repo, seed, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("SyncSince: %v", err)
	}

	if merges := outsideMerges(t); len(merges) != 1 || merges[0].Detail["sha"] != merge {
		t.Fatalf("outside merge events = %+v, want the hand merge %s", merges, merge)
	}
}

func TestSyncSince_RefusesARefThatDoesNotResolve(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	err := SyncSince(clone, "no-such-ref", false, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no-such-ref") {
		t.Fatalf("SyncSince = %v, want an error naming the bad ref", err)
	}
	if evs := emitted(t); len(evs) != 0 {
		t.Fatalf("events = %+v, want none", evs)
	}
}

// The post-merge hook runs these reads with its own GIT_DIR and GIT_INDEX_FILE
// in the environment; the scan reads the repository it was given, whatever
// they name.
func TestTrunkMergesIn_IsSealedAgainstAHooksGitEnvironment(t *testing.T) {
	repo := initRepo(t)
	spawnGit(t, repo, "commit", "-q", "--allow-empty", "-m", "Add the thing (#5)")
	other := initRepo(t)
	want := spawnGit(t, repo, "rev-parse", "HEAD")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	merges, err := trunkMergesIn(repo, "HEAD~1..HEAD")

	if err != nil || len(merges) != 1 || merges[0].PR != 5 {
		t.Fatalf("trunkMergesIn = %+v, %v; want the squash merge of #5 from the repo it was given", merges, err)
	}
	if tip := refTip(repo, "HEAD^{commit}"); tip == "" || tip != want {
		t.Errorf("refTip = %q, want the given repo's HEAD", tip)
	}
}
