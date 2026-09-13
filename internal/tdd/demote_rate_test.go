package tdd

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The demotion trend is a claim about a CHECK getting worse, and a raw weekly
// count cannot carry it: the box's own log grew from 21 active lanes three
// weeks ago to 309 last week, so a count that merely tracked the amount of
// work done rises for every check at once. These tests pin the three things
// that made the trend unreadable — the missing denominator, a check younger
// than the window it is judged over, and refusals from one repo summed into
// another repo's law.

// demoteLane is the worktree path a lane's entries carry, in the layout
// `workspace create` builds: <parent>/.worktrees/<repo>/<lane>.
func demoteLane(repo, lane string) string {
	return "D:/Projects/.worktrees/" + repo + "/" + lane
}

// laneRun is an ordinary run in one lane: it makes that lane visible as
// active work in its week without refusing anything, which is what a rate
// needs a denominator for.
func laneRun(now time.Time, age time.Duration, root string) string {
	return now.Add(-age).Format(time.RFC3339) + " postedit " + root + " go test ./... green 1.0s\n"
}

// laneDeny is one check refusing one edit in one lane.
func laneDeny(now time.Time, age time.Duration, root, verdict string) string {
	return now.Add(-age).Format(time.RFC3339) + " preedit " + root + " a_test.go " + verdict + " 0.0s\n"
}

// activeLanes writes n distinct lanes of a repo as active `age` ago, tagged so
// a lane in one week is never the same lane as in another.
func activeLanes(now time.Time, age time.Duration, repo, tag string, n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(laneRun(now, age, demoteLane(repo, fmt.Sprintf("%s-%d", tag, i))))
	}
	return b.String()
}

// denyIn refuses n times in a repo's lanes `age` ago, one refusal per lane so
// the denials sit in lanes activeLanes already named.
func denyIn(now time.Time, age time.Duration, repo, tag, check string, n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(laneDeny(now, age, demoteLane(repo, fmt.Sprintf("%s-%d", tag, i)), "pretooluse-denied:"+check))
	}
	return b.String()
}

const (
	week0 = 3 * 24 * time.Hour  // inside the last 7 days
	week1 = 10 * 24 * time.Hour // the 7 days before that
	week2 = 17 * 24 * time.Hour // the 7 days before that
	older = 25 * 24 * time.Hour // before every window the trend is measured in
)

// A busier week refuses more of everything. test-sleep's raw count rises every
// week here (1, 3, 6) while its refusals PER ACTIVE LANE fall in the last week
// (0.50, 0.75, 0.375), and tautology's rate genuinely rises (0.50, 0.75,
// 0.81) on a raw count that rises no faster. Reading the raw counts names both;
// only the second is a check getting worse.
func TestDemoteCandidates_SkipsARiseThatIsOnlyABusierWeek(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	for _, w := range []struct {
		age       time.Duration
		tag       string
		lanes     int
		sleep     int
		tautology int
	}{
		{week2, "w2", 2, 1, 1},
		{week1, "w1", 4, 3, 3},
		{week0, "w0", 16, 6, 13},
	} {
		log.WriteString(activeLanes(now, w.age, "proj", w.tag, w.lanes))
		log.WriteString(denyIn(now, w.age, "proj", w.tag, "test-sleep", w.sleep))
		log.WriteString(denyIn(now, w.age, "proj", w.tag, "tautology", w.tautology))
	}
	// Both checks are older than the oldest window, so history is not what
	// separates them here — the denominator is.
	log.WriteString(activeLanes(now, older, "proj", "old", 1))
	log.WriteString(denyIn(now, older, "proj", "old", "test-sleep", 1))
	log.WriteString(denyIn(now, older, "proj", "old", "tautology", 1))

	got := DemoteCandidates(strings.NewReader(log.String()), now)
	want := []DemoteCandidate{{Repo: "D:/Projects/proj", Check: "tautology"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("candidates = %v, want %v — test-sleep refused 6 times in 16 lanes (0.38/lane) after 3 in 4 (0.75/lane), which is a fall, not a rise", got, want)
	}
}

// A check that did not exist three weeks ago has an empty oldest week, and
// "more than the week before" is then satisfied by its own first refusal. The
// box's whole ratchet vocabulary went in on one day and every law in it was
// named a demotion candidate the following week on exactly this reading.
func TestDemoteCandidates_SkipsACheckWithNoHistoryInTheOldestWindow(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	// The repo is busy in all three weeks, so the check's absence from the
	// oldest one is the check's age, not a quiet week.
	log.WriteString(activeLanes(now, week2, "proj", "w2", 4))
	log.WriteString(activeLanes(now, week1, "proj", "w1", 4))
	log.WriteString(activeLanes(now, week0, "proj", "w0", 4))
	log.WriteString(denyIn(now, week1, "proj", "w1", "brand-new-law", 1))
	log.WriteString(denyIn(now, week0, "proj", "w0", "brand-new-law", 3))

	if got := DemoteCandidates(strings.NewReader(log.String()), now); len(got) != 0 {
		t.Fatalf("candidates = %v, want none — brand-new-law's first refusal ever is 10 days old, so its oldest week is absence of the CHECK, not absence of refusals", got)
	}
}

// 43 of the 75 refusals in one real week came from a repo whose laws this
// checkout does not carry. Summed into one trend line they moved a law this
// repo has never run, and the issue was opened here.
func TestDemoteCandidates_KeepsEachReposTrendApart(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	for _, w := range []struct {
		age   time.Duration
		tag   string
		lanes int
		other int
		mine  int
	}{
		{week2, "w2", 4, 1, 2},
		{week1, "w1", 4, 2, 2},
		{week0, "w0", 4, 3, 2},
	} {
		log.WriteString(activeLanes(now, w.age, "borld", w.tag, w.lanes))
		log.WriteString(activeLanes(now, w.age, "aphrollo-tools", w.tag, w.lanes))
		log.WriteString(denyIn(now, w.age, "borld", w.tag, "comment-hygiene", w.other))
		log.WriteString(denyIn(now, w.age, "aphrollo-tools", w.tag, "comment-hygiene", w.mine))
	}
	log.WriteString(activeLanes(now, older, "borld", "old", 1))
	log.WriteString(denyIn(now, older, "borld", "old", "comment-hygiene", 1))
	log.WriteString(activeLanes(now, older, "aphrollo-tools", "old", 1))
	log.WriteString(denyIn(now, older, "aphrollo-tools", "old", "comment-hygiene", 1))

	got := DemoteCandidates(strings.NewReader(log.String()), now)
	want := DemoteCandidate{Repo: "D:/Projects/borld", Check: "comment-hygiene"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("candidates = %v, want exactly %v — the same check name is a different law in each repo, and only borld's rose (1, 2, 3 against a flat 2, 2, 2 here)", got, want)
	}
}

// A refusal the log could not place — the pre-edit hook writes `-` for a tool
// payload carrying no path — belongs to no repo, so there is no repo whose
// law it could be and nowhere its issue could honestly be filed.
func TestDemoteCandidates_DropsRefusalsWithNoRepoToAttributeThemTo(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	log.WriteString(activeLanes(now, week2, "proj", "w2", 4))
	log.WriteString(activeLanes(now, week1, "proj", "w1", 4))
	log.WriteString(activeLanes(now, week0, "proj", "w0", 4))
	for _, n := range []struct {
		age time.Duration
		n   int
	}{{older, 1}, {week2, 1}, {week1, 2}, {week0, 3}} {
		for range n.n {
			log.WriteString(laneDeny(now, n.age, "-", "pretooluse-denied:dev-instrument-registry"))
		}
	}

	if got := DemoteCandidates(strings.NewReader(log.String()), now); len(got) != 0 {
		t.Fatalf("candidates = %v, want none — every one of those refusals was logged with root `-`", got)
	}
}

// The log carries whatever spelling each caller passed — `D:\Projects\borld`
// from one hook, `d:/projects/borld` from another. On a case-insensitive host
// those are one checkout, and read as two they do not merely split a trend in
// half: each half then has a window with nothing in it, which is no evidence
// at all and refuses to judge.
func TestDemoteCandidates_FoldsTwoSpellingsOfOnePathOnlyWhereThePlatformDoes(t *testing.T) {
	now := time.Now().UTC()
	var log strings.Builder
	// The oldest window and the check's history are written one way, the two
	// recent windows the other.
	log.WriteString(laneRun(now, older, "D:/Projects/proj"))
	log.WriteString(laneDeny(now, older, "D:/Projects/proj", "pretooluse-denied:test-sleep"))
	log.WriteString(laneRun(now, week2, "D:/Projects/proj"))
	for _, w := range []struct {
		age time.Duration
		n   int
	}{{week1, 1}, {week0, 2}} {
		log.WriteString(laneRun(now, w.age, "d:/projects/PROJ"))
		for range w.n {
			log.WriteString(laneDeny(now, w.age, "d:/projects/PROJ", "pretooluse-denied:test-sleep"))
		}
	}

	for _, tc := range []struct {
		goos string
		want int
	}{{"windows", 1}, {"linux", 0}} {
		t.Run(tc.goos, func(t *testing.T) {
			restore := demotePathGOOSFn
			demotePathGOOSFn = func() string { return tc.goos }
			t.Cleanup(func() { demotePathGOOSFn = restore })

			got := DemoteCandidates(strings.NewReader(log.String()), now)
			if len(got) != tc.want {
				t.Fatalf("candidates = %v, want %d on %s", got, tc.want, tc.goos)
			}
			if tc.want == 1 && got[0].Check != "test-sleep" {
				t.Fatalf("candidate = %v, want the one check both spellings refused", got[0])
			}
		})
	}
}

// The issue names a law, and a law belongs to the repo that declares it.
// Filing another repo's trend here asks this repo's owner to judge a rule
// their tree does not contain.
func TestRecordDemoteCandidates_FilesNothingForAnotherReposLaw(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGhScript(t, map[string]string{
		"issue list":   `[]`,
		"issue create": "https://github.com/o/r/issues/5",
	})

	n := RecordDemoteCandidates(repo, []DemoteCandidate{{Repo: "D:/Projects/borld", Check: "comment-hygiene"}}, &strings.Builder{})
	if n != 0 {
		t.Fatalf("opened %d issues for a law that lives in another checkout, want 0", n)
	}
	if strings.Contains(ghArgv(t, log), "issue create") {
		t.Errorf("borld's law must not become an issue on this repo's tracker:\n%s", ghArgv(t, log))
	}
}
