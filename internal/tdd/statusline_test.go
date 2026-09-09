package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ansi strips SGR escapes so a test asserts on what a human reads, not on the
// colour bytes around it. One test below asserts the colours themselves.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// statusPayload is the statusline input Claude Code writes on stdin.
func statusPayload(t *testing.T, session, cwd string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"session_id": session, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// statusRoot is a project dir with a root marker and an isolated config dir,
// so the badge reads the same state the hooks write.
func statusRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// ratchet: test_removed TestStatusLine_IsAQuietBadgeWhenNothingIsWrong: renamed, not
// deleted. It pinned a bare badge for a session that had measured nothing, which is the
// behaviour this change removes — that state now reads unproven. The claim it made lives
// on below under the name the new contract deserves.
//
// TestStatusLine_SaysUnprovenBeforeAnythingHasBeenMeasured pins the default
// for a session that has measured nothing yet. The badge used to render this
// state as a bare green, identical to a suite that had just passed, which made
// the absence of a measurement look like a good one. The suffix stays reserved
// for what matters, and "nobody has tested this tree" matters.
func TestStatusLine_SaysUnprovenBeforeAnythingHasBeenMeasured(t *testing.T) {
	root := statusRoot(t)
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo:unproven]" {
		t.Fatalf("StatusLine = %q, want %q", got, "[aphrollo:unproven]")
	}
}

// TestStatusLine_SaysOffInTextSoAStrippedBadgeCannotReadAsArmed is the one
// state a session must never lose track of: with `/gate off` set, edits are
// not gated at all. Colour alone cannot carry it -- a statusline that strips
// SGR, a log, a screenshot in a terminal with a different palette all render
// the gray badge as the green one, and the session reads an ungated tree as
// gated. Red and green stay colour-only: both mean the gate is running.
func TestStatusLine_SaysOffInTextSoAStrippedBadgeCannotReadAsArmed(t *testing.T) {
	root := statusRoot(t)
	if err := setOff("s1", true); err != nil {
		t.Fatal(err)
	}
	got := StatusLine(statusPayload(t, "s1", root))
	if !strings.HasPrefix(got, ansiGray) {
		t.Fatalf("a disabled gate must render gray, got %q", got)
	}
	if plain(got) != "[aphrollo:off]" {
		t.Fatalf("StatusLine = %q, want the state in the text too", plain(got))
	}
}

// TestStatusLine_ColoursGreenWhenOnAndGrayWhenOff pins the at-a-glance signal:
// the two states must be distinguishable without reading the text.
func TestStatusLine_ColoursGreenWhenOnAndGrayWhenOff(t *testing.T) {
	root := statusRoot(t)
	on := StatusLine(statusPayload(t, "s1", root))
	if !strings.HasPrefix(on, "\x1b[32m") {
		t.Fatalf("an armed gate must render green, got %q", on)
	}
	if err := setOff("s1", true); err != nil {
		t.Fatal(err)
	}
	off := StatusLine(statusPayload(t, "s1", root))
	if !strings.HasPrefix(off, "\x1b[90m") {
		t.Fatalf("a disabled gate must render gray, got %q", off)
	}
}

// TestStatusLine_ReportsTheLastRedOutcomeForThisProject surfaces the fact a
// session most often loses across a compaction: the last edit left the suite
// red and nothing on screen says so. The badge itself carries it, in colour.
func TestStatusLine_ReportsTheLastRedOutcomeForThisProject(t *testing.T) {
	root := statusRoot(t)
	stampOutcomeAt(t, "s1", root, string(Red), time.Now())

	got := StatusLine(statusPayload(t, "s1", root))
	if !strings.HasPrefix(got, ansiRed) {
		t.Fatalf("a red outcome must colour the badge red, got %q", got)
	}
	if plain(got) != "[aphrollo]" {
		t.Fatalf("StatusLine = %q, want the bare badge in red", plain(got))
	}
}

// TestStatusLine_CarriesNoRedWord is the rule the colour exists to serve: the
// state is a colour, not a word a session has to read and re-read. A `red`
// suffix is what the badge used to print.
func TestStatusLine_CarriesNoRedWord(t *testing.T) {
	root := statusRoot(t)
	stampOutcomeAt(t, "s1", root, string(Red), time.Now())

	if got := plain(StatusLine(statusPayload(t, "s1", root))); strings.Contains(got, "red") {
		t.Fatalf("StatusLine = %q, want no `red` word — the colour says it", got)
	}
}

// TestStatusLine_APrecommitGreenAfterAPostEditRedRendersGreen is the stale-red
// case a session hits every time a fix lands through a commit: the post-edit
// hook recorded red, the commit gate then ran everything and passed, and a
// badge that still reads red is reporting a failure that no longer exists.
// ANY green outcome for this project clears it, from any stage.
func TestStatusLine_APrecommitGreenAfterAPostEditRedRendersGreen(t *testing.T) {
	root := statusRoot(t)
	stampOutcomeAt(t, "s1", root, string(Red), time.Now().Add(-time.Minute))
	appendGateLog("precommit", root, "cargo nextest run", "green", 12*time.Second)

	got := StatusLine(statusPayload(t, "s1", root))
	if !strings.HasPrefix(got, ansiGreen) {
		t.Fatalf("a later green must clear the red, got %q", got)
	}
}

// TestStatusLine_AGreenInAnotherProjectLeavesTheRedStanding keeps the clearing
// rule as narrow as the red itself: another repo's green says nothing about
// this one.
func TestStatusLine_AGreenInAnotherProjectLeavesTheRedStanding(t *testing.T) {
	root := statusRoot(t)
	stampOutcomeAt(t, "s1", root, string(Red), time.Now().Add(-time.Minute))
	appendGateLog("precommit", filepath.Join(t.TempDir(), "other"), "cargo nextest run", "green", time.Second)

	if got := StatusLine(statusPayload(t, "s1", root)); !strings.HasPrefix(got, ansiRed) {
		t.Fatalf("StatusLine = %q, want the red to stand", got)
	}
}

// TestStatusLine_ARedOlderThanTheWindowRendersGreen states the other half of
// "real-time or nothing": an hour-old red with nothing after it describes a
// tree the session has moved far past, and a badge nobody trusts is worse than
// no badge. The age is written out rather than derived from the constant the
// subject reads, so widening the window fails this test instead of moving it.
func TestStatusLine_ARedOlderThanTheWindowRendersGreen(t *testing.T) {
	root := statusRoot(t)
	stampOutcomeAt(t, "s1", root, string(Red), time.Now().Add(-31*time.Minute))

	got := StatusLine(statusPayload(t, "s1", root))
	if !strings.HasPrefix(got, ansiGreen) {
		t.Fatalf("a red past the window must render green, got %q", got)
	}
	if plain(got) != "[aphrollo:unproven]" {
		t.Fatalf("StatusLine = %q, want the unproven badge — the stale red is dropped, but the tree "+
			"it described has still not been measured since, and a bare badge would claim it passed", plain(got))
	}
}

// The gate log is space-separated and the command in the middle contains
// spaces, so a REPO ROOT with a space in it (`C:/My Projects/borld`, and
// every checkout under "Program Files") split into two fields: no entry ever
// matched the badge's root again, a red was never cleared by a green, and a
// queued run was never seen either.
func TestStatusLine_ClearsAredForAProjectWhosePathHasASpace(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "my repo")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stampOutcomeAt(t, "s1", root, string(Red), time.Now().Add(-time.Minute))
	appendGateLog("precommit", root, "cargo nextest run", "green", 12*time.Second)

	if got := StatusLine(statusPayload(t, "s1", root)); !strings.HasPrefix(got, ansiGreen) {
		t.Fatalf("a later green must clear the red whatever the path looks like, got %q", got)
	}
}

// The same field split hid a QUEUED-SKIPPED run, which is the outcome most
// easily mistaken for a quiet green.
func TestStatusLine_SeesAQueuedRunForAProjectWhosePathHasASpace(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "my repo")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	appendGateLog("postedit", root, "cargo nextest run", "queued-skipped", 0)

	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo:queued]" {
		t.Fatalf("StatusLine = %q, want the queued tag", got)
	}
}

// TestStatusLine_LastRunQueuedMatchesARootLoggedWithDifferentCasing pins that
// lastRunQueued routes its root comparison through sameProject, the same way
// greenLoggedSince already does. Off Windows two differently-cased paths name
// different files, so the queued badge is right to miss them; on Windows they
// name the same tree, and the badge missing it is the queued run silently
// reading as a quiet green.
func TestStatusLine_LastRunQueuedMatchesARootLoggedWithDifferentCasing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "myrepo")
	appendGateLog("postedit", root, "cargo nextest run", "queued-skipped", 0)

	differentCase := strings.ToUpper(root)
	got := lastRunQueued(differentCase)
	if runtime.GOOS == "windows" {
		if !got {
			t.Fatal("lastRunQueued must case-fold on Windows, or a queued run logged under one casing vanishes for every other")
		}
		return
	}
	if got {
		t.Fatal("lastRunQueued must stay case-SENSITIVE off Windows")
	}
}

// stampOutcomeAt records an outcome for root at a chosen time, which is what
// a staleness rule needs and `stamp` (always now) cannot give.
func stampOutcomeAt(t *testing.T, session, root, outcome string, at time.Time) {
	t.Helper()
	s, path := loadSession(session)
	s.ByProject[root] = projectState{Outcome: outcome, TS: at.UTC().Format(time.RFC3339)}
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
}

// TestStatusLine_IgnoresAnotherProjectsRed keeps the badge about the project
// the session is standing in: a red left in an unrelated repo earlier in the
// session is not this repo's state.
func TestStatusLine_IgnoresAnotherProjectsRed(t *testing.T) {
	root := statusRoot(t)
	s, path := loadSession("s1")
	s.stamp(filepath.Join(t.TempDir(), "other"), projectState{Outcome: string(Red)})
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo:unproven]" {
		t.Fatalf("StatusLine = %q, want no red — another project's red is not this one's state, and "+
			"this one has measured nothing", got)
	}
}

// TestStatusLine_ReportsARunningDeferredBuild answers the question a session
// asks while a detached build runs: is anything happening, or did the gate
// simply say nothing?
func TestStatusLine_ReportsARunningDeferredBuild(t *testing.T) {
	root := statusRoot(t)
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", Started: time.Now(), Session: "s1"})
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo:deferred]" {
		t.Fatalf("StatusLine = %q, want %q", got, "[aphrollo:deferred]")
	}
}

// TestStatusLine_DropsDeferredOnceTheResultLanded is the other half: the
// result file's existence IS the liveness signal, so a finished job must stop
// claiming a build is running.
func TestStatusLine_DropsDeferredOnceTheResultLanded(t *testing.T) {
	root := statusRoot(t)
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", Started: time.Now(), Session: "s1"})
	j, ok := loadDeferredJob("s1", root)
	if !ok {
		t.Fatal("setup: job not saved")
	}
	writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo:unproven]" {
		t.Fatalf("StatusLine = %q, want the deferred tag gone — a landed build result stops the badge "+
			"claiming a build is running, and it is not itself a suite that passed", got)
	}
}

// TestStatusLine_ReportsThatTheLastRunOnlyQueued names the outcome most easily
// mistaken for green: QUEUED-SKIPPED means the suite never ran at all.
func TestStatusLine_ReportsThatTheLastRunOnlyQueued(t *testing.T) {
	root := statusRoot(t)
	appendGateLog("postedit", root, "cargo nextest run", "queued-skipped", 0)
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo:queued]" {
		t.Fatalf("StatusLine = %q, want %q", got, "[aphrollo:queued]")
	}
}

// TestStatusLine_ForgetsAQueuedRunOnceOneActuallyRan pins that the suffix
// reads the LAST run for this project, not any queued run ever.
func TestStatusLine_ForgetsAQueuedRunOnceOneActuallyRan(t *testing.T) {
	root := statusRoot(t)
	appendGateLog("postedit", root, "cargo nextest run", "queued-skipped", 0)
	appendGateLog("postedit", root, "cargo nextest run", "green", time.Second)
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo]" {
		t.Fatalf("StatusLine = %q, want a bare badge", got)
	}
}

// TestStatusLine_MalformedPayloadStillRendersABadge keeps the statusline from
// ever being the thing that breaks: it runs on every prompt render and has no
// way to report an error.
func TestStatusLine_MalformedPayloadStillRendersABadge(t *testing.T) {
	statusRoot(t)
	if got := plain(StatusLine([]byte("{not json"))); got != "[aphrollo]" {
		t.Fatalf("StatusLine = %q, want a bare badge", got)
	}
}
