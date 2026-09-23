package tdd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// The defect this file exists for: the gate ran the suite, kept the VERDICT
// and threw the OUTPUT away. A session that wants the assertion text the run
// already printed then has no route to it — `gate stats` holds a word, not a
// line of test output — so it reaches for a hand rerun the narrowing rule
// refuses, and reworded commands are what follow. Retaining the bytes the
// gate itself captured is the answer; every test below is about those bytes
// still being there afterwards.
func TestPostEdit_RetainsTheOutputOfTheRunItMade(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	output := "--- FAIL: TestWidget\n    specimen.rs:355: assertion failed: left == right\n"

	PostEdit(postPayload("Edit", src), fakeRun(false, output))

	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput after a settled post-edit run: %v", err)
	}
	// The header is what lets a reader trust the bytes: which run this was,
	// how old, under what command, and what it was judged to be.
	for _, want := range []string{"specimen.rs:355", "postedit", root, "stage:", "verdict:", "command:", "duration:", "at:"} {
		if !strings.Contains(got, want) {
			t.Errorf("retained record must name %q:\n%s", want, got)
		}
	}
}

// A failure prints at the END of a run, so a cap that kept the head would
// throw away the only part worth keeping. Keep the tail — and say in the
// file that the head is gone, because a reader silently handed a fragment
// draws conclusions from a run they cannot see the start of.
func TestRetainSuiteOutput_KeepsTheTailAndMarksTheTruncation(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	head := "FIRST-LINE-OF-A-VERY-LONG-RUN\n"
	tail := "--- FAIL: TestLast\n    the line that matters\n"
	output := head + strings.Repeat("filler filler filler\n", suiteOutputCap/16) + tail

	retainSuiteOutput("postedit", root, "go test ./...", "red", SuiteResult{Output: output})

	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput: %v", err)
	}
	if !strings.Contains(got, tail) {
		t.Errorf("the tail of the run must survive the cap:\n%s", lastBytes(got, 400))
	}
	if strings.Contains(got, head) {
		t.Error("the head must be the part dropped, not the tail")
	}
	if !strings.Contains(got, "TRUNCATED") {
		t.Errorf("a truncated record must say so:\n%s", firstBytes(got, 400))
	}
	if len(got) > suiteOutputCap+4096 {
		t.Errorf("record is %d bytes, past the %d cap plus header slack", len(got), suiteOutputCap)
	}
}

// Keyed per repo root: a run in one checkout must never answer a question
// asked about another. Two lanes of the same repo are two roots here, which
// is the case that would otherwise show a session output from a tree it is
// not standing in.
func TestRetainedSuiteOutput_OneRootNeverAnswersForAnother(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	mine := mkProject(t, "go.mod")
	theirs := mkProject(t, "go.mod")

	retainSuiteOutput("postedit", theirs, "go test ./...", "red", SuiteResult{Output: "THEIR-FAILURE\n"})

	if _, err := RetainedSuiteOutput(mine); err == nil {
		t.Fatal("a run recorded for another root must not answer for this one")
	}
}

// The refusal this record answers is bounded by the same freshness window
// (bashSuiteVerdictFreshFor); output older than that describes a tree the
// session is no longer standing in, and handing it over as if it were
// current is the same lie the missing output caused.
func TestRetainedSuiteOutput_RefusesARecordOlderThanTheFreshnessWindow(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")

	if err := writeSuiteOutputRecord(suiteOutputRecord{
		At: time.Now().UTC().Add(-2 * bashSuiteVerdictFreshFor), Stage: "postedit", Root: root,
		Cmd: "go test ./...", Verdict: "red", Output: "STALE-FAILURE\n",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := RetainedSuiteOutput(root)
	if err == nil {
		t.Fatalf("a record past the freshness window must not be served as current:\n%s", got)
	}
	if !strings.Contains(err.Error(), "older than") {
		t.Errorf("the refusal must name staleness as the reason, got %q", err)
	}
}

// The record is a cache of the last SETTLED run, and a verdict that carries
// no output of its own (a ratchet or docs block, a stage that never spawned
// a runner) is not one: overwriting with nothing would delete the very text
// a session is about to ask for.
func TestRetainSuiteOutput_KeepsTheRunWhenAVerdictWithNoOutputFollows(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")

	retainSuiteOutput("postedit", root, "go test ./...", "red", SuiteResult{Output: "REAL-FAILURE\n"})
	retainSuiteOutput("precommit", root, "ratchet check", "ratchet-rejected", SuiteResult{})

	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput: %v", err)
	}
	if !strings.Contains(got, "REAL-FAILURE") {
		t.Errorf("an output-less verdict must not clobber the retained run:\n%s", got)
	}
}

// A run recorded for a crate NESTED in the checkout answers a question asked
// from the checkout root: the gate keys on the nearest marker directory (a
// Cargo workspace member), while a session types the command wherever it is
// standing.
func TestRetainedSuiteOutput_ANestedRootAnswersItsCheckout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	checkout := mkProject(t, "go.mod")
	member := filepath.Join(checkout, "crates", "forge")
	write(t, member, "Cargo.toml", "[package]\nname = \"forge\"\n")

	retainSuiteOutput("postedit", member, "cargo nextest run -p forge", "red", SuiteResult{Output: "MEMBER-FAILURE\n"})

	got, err := RetainedSuiteOutput(checkout)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput from the checkout root: %v", err)
	}
	if !strings.Contains(got, "MEMBER-FAILURE") {
		t.Errorf("a nested root's run must answer its checkout:\n%s", got)
	}
}

// A store that cannot be written must never change a gate decision — the
// same best-effort posture appendGateLog owes, for the same reason: losing
// the trail is bad, losing the verdict is worse.
func TestRetainSuiteOutput_IsBestEffortWhenThereIsNoStateDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	root := mkProject(t, "go.mod")

	retainSuiteOutput("postedit", root, "go test ./...", "red", SuiteResult{Output: "boom"})

	if _, err := RetainedSuiteOutput(root); err == nil {
		t.Fatal("with no state dir there is nothing to serve")
	}
}

// The refusal used to name ONE route, `aphrollo gate stats`, and claim that
// re-running answers nothing that line does not. False whenever the session
// does not want a verdict: a session that needed the assertion text the gate
// had printed 30 seconds earlier could get it from neither the log nor the
// refusal, so it brute-forced the guard with a six-shot loop instead. Both
// routes have to be named, and which answers which, or the refusal is still
// a dead end for the question that was actually asked.
func TestDenyNarrowedRerunReason_NamesBothRoutesAndWhichAnswersWhich(t *testing.T) {
	root := mkProject(t, "go.mod")
	reason := denyNarrowedRerunReason(root, gateEntry{
		At: time.Now().Add(-90 * time.Second), Stage: "postedit", Root: root, Verdict: "red",
	})

	for _, want := range []string{
		"aphrollo gate stats",  // the verdict
		"aphrollo gate output", // the text that run actually printed
		"red", "postedit", "ago",
		"INCONCLUSIVE", "TIMEOUT", "SKIPPED", "QUEUED-SKIPPED",
		string(DeferredAbandoned), string(InfraFailed),
		"MUTATION=1",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal must name %q:\n%s", want, reason)
		}
	}
	// Naming both is not enough — a session has to be able to tell which
	// question each answers without running one to find out.
	stats := strings.Index(reason, "`aphrollo gate stats` for the verdict")
	output := strings.Index(reason, "`aphrollo gate output` for the")
	if stats < 0 || output < 0 {
		t.Fatalf("each route must be named with the question it answers:\n%s", reason)
	}
}

// The block `aphrollo install` writes into every repo's CLAUDE.md is where a
// session learns the manual-run rules, and it told them to read `gate stats`
// and nothing else. A session that wants the run's TEXT has to be told the
// route exists there too, or it meets the refusal never having heard of it.
func TestClaudeMDBlock_NamesTheRouteToTheRunsOwnText(t *testing.T) {
	block := ClaudeMDBlock(shimDir, false, false)
	if !strings.Contains(block, "aphrollo gate output") {
		t.Errorf("the block must name the route to the run's own text:\n%s", block)
	}
}

func firstBytes(s string, n int) string { return tddtest.FirstBytes(s, n) }

func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
