package merge

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures under testdata/retro/<pr>/ are gh's own output for four
// merged PRs of this repo, recorded once with the exact argv the collector
// sends: #839 (a mutants-verdict red with five survivors, one push after
// open), #832 (a test job red, one push after open), #827 (clean) and #823
// (a run that passed on its second attempt). The job log is the tail of the
// real `--log-failed` output, where the summary lines sit.

// replayGh answers the collector's gh calls from pr's recorded fixtures.
// patch replaces a fixture's content by file name; a call with no fixture
// fails the test by argv, so an unexpected call is never silently answered.
func replayGh(t *testing.T, pr string, patch map[string]string) *[]string {
	t.Helper()
	var calls []string
	prev := retroGh
	retroGh = func(dir string, timeout time.Duration, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if timeout <= 0 || timeout > retroBudget {
			t.Errorf("gh %v ran with timeout %s, want within (0, %s]", args, timeout, retroBudget)
		}
		name := ""
		switch {
		case len(args) > 2 && args[0] == "pr" && args[1] == "view":
			if args[2] != pr {
				t.Errorf("gh pr view asked for #%s, want #%s", args[2], pr)
			}
			name = "pr-view.json"
		case len(args) > 1 && args[0] == "run" && args[1] == "list":
			name = "run-list.json"
		case len(args) > 3 && args[0] == "run" && args[1] == "view" && args[2] == "--job":
			name = "job-log-" + args[3] + ".log"
		case len(args) > 2 && args[0] == "run" && args[1] == "view":
			name = "run-view-" + args[2] + ".json"
		}
		if body, ok := patch[name]; ok {
			return body, nil
		}
		data, err := os.ReadFile(filepath.Join("testdata", "retro", pr, name))
		if err != nil {
			t.Errorf("unrecorded gh call %v: %v", args, err)
			return "", err
		}
		return string(data), nil
	}
	t.Cleanup(func() { retroGh = prev })
	return &calls
}

// isolateRetro gives the test its own state dir and names the session the
// merge runs in ("" for none).
func isolateRetro(t *testing.T, session string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", session)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
}

// logGate appends gate.log lines for root, one per "<RFC3339> <stage> <verdict>".
func logGate(t *testing.T, root string, entries ...string) {
	t.Helper()
	dir := StateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		f := strings.Fields(e)
		b.WriteString(f[0] + " " + f[1] + " " + LogToken(root) + " go test ./x " + f[2] + " 1.0s\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "gate.log"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runRetro(t *testing.T, worktree, branch string, pr int) string {
	t.Helper()
	var stderr bytes.Buffer
	PostMergeRetro(worktree, worktree, branch, pr, &stderr)
	return stderr.String()
}

func TestPostMergeRetro_CleanMergePrintsAndRecordsNothing(t *testing.T) {
	isolateRetro(t, "sess-clean")
	replayGh(t, "827", nil)
	lane := t.TempDir()
	logGate(t, lane, "2026-09-24T08:20:00Z postedit green")

	if msg := runRetro(t, lane, "lane/own-tests-smell", 827); msg != "" {
		t.Errorf("a clean merge printed %q, want nothing", msg)
	}
	if got := TakeSessionRetros("sess-clean"); got != "" {
		t.Errorf("a clean merge recorded a retro:\n%s", got)
	}
}

func TestPostMergeRetro_SurvivorsAfterLocalGreenAndExtraPush(t *testing.T) {
	isolateRetro(t, "sess-839")
	replayGh(t, "839", nil)
	lane := t.TempDir()
	logGate(t, lane, "2026-09-24T11:50:00Z postedit green")

	if msg := runRetro(t, lane, "lane/probe-discard", 839); msg != "" {
		t.Errorf("the merge printed %q; the retro is delivered by the next hook, not the merge", msg)
	}
	got := TakeSessionRetros("sess-839")
	for _, want := range []string{
		"retro #839 lane/probe-discard (43m open→merge):",
		"#839: CI mutants-verdict red (5 survivors) after local green",
		"#839: 1 push after open",
		"? which local stage or law would have caught this? → aphrollo gate escape record \"<reason>\"",
		"? what rule avoids the extra push? → a memory/feedback note or an issue",
		"a retro answered only in prose is not answered",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("retro lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "timed out") || strings.Contains(got, "unmeasured") {
		t.Errorf("a run with 0 timeouts and 0 unmeasured reported them:\n%s", got)
	}
}

func TestPostMergeRetro_CIRedAfterLocalGreen(t *testing.T) {
	isolateRetro(t, "sess-832")
	replayGh(t, "832", nil)
	lane := t.TempDir()
	logGate(t, lane, "2026-09-24T09:12:00Z precommit green")

	runRetro(t, lane, "lane/own-tests-lock", 832)
	got := TakeSessionRetros("sess-832")
	if !strings.Contains(got, "#832: CI test red after local green\n") {
		t.Errorf("retro lacks the CI red after the local green:\n%s", got)
	}
}

func TestPostMergeRetro_CIRedWithNoLocalGreenBeforeIt(t *testing.T) {
	isolateRetro(t, "sess-832b")
	replayGh(t, "832", nil)
	lane := t.TempDir()
	// A green logged AFTER the run started did not precede it.
	logGate(t, lane, "2026-09-24T09:14:30Z postedit green")

	runRetro(t, lane, "lane/own-tests-lock", 832)
	got := TakeSessionRetros("sess-832b")
	if !strings.Contains(got, "#832: CI test red with no local green before it\n") {
		t.Errorf("retro lacks the CI red with no local green:\n%s", got)
	}
	if strings.Contains(got, "after local green") {
		t.Errorf("a green logged after the run was read as preceding it:\n%s", got)
	}
}

func TestPostMergeRetro_RunThatPassedOnARerunIsAFlake(t *testing.T) {
	isolateRetro(t, "sess-823")
	replayGh(t, "823", nil)

	runRetro(t, t.TempDir(), "lane/cachehit-verify", 823)
	got := TakeSessionRetros("sess-823")
	if !strings.Contains(got, "#823: Pipeline passed on attempt 2 after a failed attempt\n") {
		t.Errorf("retro lacks the flaky re-run:\n%s", got)
	}
	if !strings.Contains(got, "? which test flaked, and what makes it deterministic?") {
		t.Errorf("retro lacks the flake question:\n%s", got)
	}
}

func TestPostMergeRetro_MutantTimeoutsAndUnmeasuredAreCounted(t *testing.T) {
	isolateRetro(t, "sess-timeout")
	data, err := os.ReadFile(filepath.Join("testdata", "retro", "839", "job-log-107620766328.log"))
	if err != nil {
		t.Fatal(err)
	}
	// The recorded run had none of either; this derives one that had some.
	log := strings.ReplaceAll(string(data), "Timed out: 0,", "Timed out: 3,")
	log = strings.ReplaceAll(log, "(0 accepted), 0 unmeasured", "(0 accepted), 2 unmeasured")
	replayGh(t, "839", map[string]string{"job-log-107620766328.log": log})

	runRetro(t, t.TempDir(), "lane/probe-discard", 839)
	got := TakeSessionRetros("sess-timeout")
	if !strings.Contains(got, "#839: CI mutants-verdict red (5 survivors, 3 timed out, 2 unmeasured) with no local green before it\n") {
		t.Errorf("retro lacks the timeout and unmeasured counts:\n%s", got)
	}
	if !strings.Contains(got, "? which test or budget let mutants time out unmeasured?") {
		t.Errorf("retro lacks the mutant-timeout question:\n%s", got)
	}
}

func TestPostMergeRetro_SlowMergeIsJudgedAgainstTheDeclaredMinutes(t *testing.T) {
	isolateRetro(t, "sess-slow")
	replayGh(t, "827", nil)
	lane := t.TempDir()
	write(t, lane, "aphrollo.toml", "[aphrollo]\nretro-slow-merge-minutes = 5\n")

	runRetro(t, lane, "lane/own-tests-smell", 827)
	got := TakeSessionRetros("sess-slow")
	if !strings.Contains(got, "#827: 5m open→merge, over the 5m bar\n") {
		t.Errorf("retro lacks the slow merge:\n%s", got)
	}
}

func TestPostMergeRetro_MergeUnderTheSlowBarIsSilent(t *testing.T) {
	isolateRetro(t, "sess-fast")
	replayGh(t, "827", nil)
	lane := t.TempDir()
	write(t, lane, "aphrollo.toml", "[aphrollo]\nretro-slow-merge-minutes = 6\n")

	runRetro(t, lane, "lane/own-tests-smell", 827)
	if got := TakeSessionRetros("sess-fast"); got != "" {
		t.Errorf("a 5m merge under a 6m bar recorded a retro:\n%s", got)
	}
}

func TestPostMergeRetro_ConflictResolvedInTheLaneIsCounted(t *testing.T) {
	isolateRetro(t, "sess-conflict")
	replayGh(t, "827", nil)
	repo := t.TempDir()
	gitInit(t, repo)
	gitDo(t, repo, "checkout", "-q", "-B", "main")
	write(t, repo, "a.txt", "base\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "base")
	gitDo(t, repo, "checkout", "-q", "-b", "lane/own-tests-smell")
	write(t, repo, "a.txt", "lane\n")
	gitDo(t, repo, "commit", "-qam", "lane side")
	gitDo(t, repo, "checkout", "-q", "main")
	write(t, repo, "a.txt", "main\n")
	gitDo(t, repo, "commit", "-qam", "main side")
	gitDo(t, repo, "checkout", "-q", "lane/own-tests-smell")
	if _, err := git(repo, "merge", "main"); err == nil {
		t.Fatal("the fixture merge did not conflict")
	}
	write(t, repo, "a.txt", "resolved\n")
	gitDo(t, repo, "commit", "-qam", "resolve")

	runRetro(t, repo, "lane/own-tests-smell", 827)
	got := TakeSessionRetros("sess-conflict")
	if !strings.Contains(got, "#827: 1 merge conflict resolved in the lane\n") {
		t.Errorf("retro lacks the resolved conflict:\n%s", got)
	}
}

func TestPostMergeRetro_GateRefusalsInTheLaneAreCounted(t *testing.T) {
	isolateRetro(t, "sess-refused")
	replayGh(t, "827", nil)
	lane := t.TempDir()
	logGate(t, lane,
		"2026-09-24T08:10:00Z precommit ratchet-rejected",
		"2026-09-24T08:12:00Z precommit lint-blocked",
		"2026-09-24T08:13:00Z precommit ratchet-rejected",
		"2026-09-24T08:14:00Z postedit red",
		"2026-09-24T08:15:00Z mutants mutants-refused:tested=3,caught=1",
		"2026-09-24T08:20:00Z precommit green",
	)

	runRetro(t, lane, "lane/own-tests-smell", 827)
	got := TakeSessionRetros("sess-refused")
	if !strings.Contains(got, "#827: 3 gate refusals in the lane (lint-blocked 1, ratchet-rejected 2)\n") {
		t.Errorf("retro lacks the refusals, or counted an edit-hook red or the lane's own mutation run as one:\n%s", got)
	}
}

func TestPostMergeRetro_ClassNotListedInRetroOnStaysSilent(t *testing.T) {
	isolateRetro(t, "sess-optout")
	calls := replayGh(t, "839", nil)
	lane := t.TempDir()
	write(t, lane, "aphrollo.toml", "[aphrollo]\nretro-on = [\"merge-conflict\"]\n")

	runRetro(t, lane, "lane/probe-discard", 839)
	if got := TakeSessionRetros("sess-optout"); got != "" {
		t.Errorf("friction of classes the repo did not list recorded a retro:\n%s", got)
	}
	if len(*calls) == 0 {
		t.Errorf("merge-conflict is still listed, yet nothing was collected")
	}
}

func TestPostMergeRetro_EmptyRetroOnAndNoSlowBarAsksGhNothing(t *testing.T) {
	isolateRetro(t, "sess-off")
	calls := replayGh(t, "839", nil)
	lane := t.TempDir()
	write(t, lane, "aphrollo.toml", "[aphrollo]\nretro-on = []\nretro-slow-merge-minutes = 0\n")

	runRetro(t, lane, "lane/probe-discard", 839)
	if len(*calls) != 0 {
		t.Errorf("a repo that turned every trigger off still paid for gh: %v", *calls)
	}
}

func TestPostMergeRetro_SinkMappingIsReadFromTheRepoConfig(t *testing.T) {
	isolateRetro(t, "sess-sinks")
	replayGh(t, "839", nil)
	lane := t.TempDir()
	write(t, lane, "aphrollo.toml", "[aphrollo]\nretro-sinks = [\n"+
		"  \"extra-push -> why two pushes? -> a note in the team log\",\n]\n")

	runRetro(t, lane, "lane/probe-discard", 839)
	got := TakeSessionRetros("sess-sinks")
	if !strings.Contains(got, "? why two pushes? → a note in the team log\n") {
		t.Errorf("retro ignores the repo's own sink for extra-push:\n%s", got)
	}
	if strings.Contains(got, "what rule avoids the extra push?") {
		t.Errorf("the built-in extra-push sink survived the repo's override:\n%s", got)
	}
}

func TestPostMergeRetro_GhFailureSaysSkippedOnceAndNeverRecords(t *testing.T) {
	isolateRetro(t, "sess-ghfail")
	prev := retroGh
	calls := 0
	retroGh = func(dir string, timeout time.Duration, args ...string) (string, error) {
		calls++
		return "", errors.New("HTTP 502: Bad Gateway")
	}
	t.Cleanup(func() { retroGh = prev })

	lane := t.TempDir()
	// Local friction alone must not make a retro out of a failed collection.
	logGate(t, lane, "2026-09-24T12:00:00Z precommit ratchet-rejected")

	msg := runRetro(t, lane, "lane/probe-discard", 839)
	if msg != "retro skipped: gh pr view: HTTP 502: Bad Gateway\n" {
		t.Errorf("stderr = %q, want exactly one retro skipped line naming the failure", msg)
	}
	if calls != 1 {
		t.Errorf("gh ran %d times after the first failure, want 1", calls)
	}
	if got := TakeSessionRetros("sess-ghfail"); got != "" {
		t.Errorf("a failed collection recorded a retro:\n%s", got)
	}
}

func TestPostMergeRetro_SpentBudgetStopsCollectingAndSaysSo(t *testing.T) {
	isolateRetro(t, "sess-budget")
	replayGh(t, "839", nil)
	prevBudget, prevNow := retroBudget, retroNow
	start := time.Date(2026, 9, 24, 12, 40, 0, 0, time.UTC)
	ticks := 0
	retroNow = func() time.Time {
		ticks++
		// Every reading after the first lands past the budget.
		if ticks == 1 {
			return start
		}
		return start.Add(retroBudget + time.Second)
	}
	t.Cleanup(func() { retroBudget, retroNow = prevBudget, prevNow })

	msg := runRetro(t, t.TempDir(), "lane/probe-discard", 839)
	if !strings.HasPrefix(msg, "retro skipped: gh budget of ") || strings.Count(msg, "\n") != 1 {
		t.Errorf("stderr = %q, want one retro skipped line naming the spent budget", msg)
	}
}

func TestTakeSessionRetros_DeliversExactlyOnceToTheMergingSession(t *testing.T) {
	isolateRetro(t, "sess-once")
	replayGh(t, "839", nil)
	runRetro(t, t.TempDir(), "lane/probe-discard", 839)

	if got := TakeSessionRetros("sess-other"); got != "" {
		t.Errorf("another session received the retro:\n%s", got)
	}
	if got := TakeSessionRetros("sess-once"); !strings.Contains(got, "#839:") {
		t.Fatalf("the merging session did not receive the retro: %q", got)
	}
	if got := TakeSessionRetros("sess-once"); got != "" {
		t.Errorf("the retro was delivered twice:\n%s", got)
	}
}

func TestTakeRepoRetros_SessionlessMergeWaitsForThatRepo(t *testing.T) {
	isolateRetro(t, "")
	replayGh(t, "839", nil)
	repo := t.TempDir()
	gitInit(t, repo)
	other := t.TempDir()
	gitInit(t, other)

	runRetro(t, repo, "lane/probe-discard", 839)

	if got := TakeSessionRetros(""); got != "" {
		t.Errorf("an empty session id received the retro:\n%s", got)
	}
	if got := TakeRepoRetros(other); got != "" {
		t.Errorf("another repo received the retro:\n%s", got)
	}
	if got := TakeRepoRetros(filepath.Join(repo, ".")); !strings.Contains(got, "#839:") {
		t.Fatalf("the merging repo did not receive the retro: %q", got)
	}
	if got := TakeRepoRetros(repo); got != "" {
		t.Errorf("the retro was delivered twice:\n%s", got)
	}
}
