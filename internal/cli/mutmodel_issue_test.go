package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func TestGateIssue_DryPrintsTitleLabelsAndBodyAndCallsNoGh(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer

	code := Run([]string{"gate", "issue", "the rig drifts", "--label", "physics", "--body", "seen twice", "--dry", "--repo", repo},
		strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"title: the rig drifts\n", "labels: physics\n", "body:\nseen twice\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("--dry reached gh")
	}
}

// A flag typed after the title is a flag: before, "--dry" after the title was
// folded into the title text and the issue opened anyway.
func TestGateIssue_FlagAfterTheTitleIsNotTitleText(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer

	code := Run([]string{"gate", "issue", "the rig drifts", "--dry", "--repo", repo}, strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "title: the rig drifts\n") {
		t.Errorf("the title picked up flag text:\n%s", out.String())
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("--dry after the title reached gh")
	}
}

func TestGateIssue_UnknownFlagAfterTheTitleIsRefused(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer

	code := Run([]string{"gate", "issue", "the rig drifts", "--bogus", "--repo", repo}, strings.NewReader(""), &out, &errb)

	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errb.String())
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a refused flag reached gh")
	}
}

// --dry is how to preview the undercover check: the tell is refused before
// anything is printed as a plan.
func TestGateIssue_DryStillRefusesAnUndercoverTell(t *testing.T) {
	repo, _ := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	declareUndercover(t, repo)
	var out, errb bytes.Buffer

	code := Run([]string{"gate", "issue", "Claude drifts at 60 Hz", "--dry", "--repo", repo}, strings.NewReader(""), &out, &errb)

	if code != 1 || !strings.Contains(errb.String(), "issue title") {
		t.Fatalf("exit %d, stderr %q; want the undercover refusal", code, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("a refused dry run printed a plan: %q", out.String())
	}
}

func TestGateFeedback_DryPrintsThePlanAndCallsNoGh(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/tool/issues/3")
	var out, errb bytes.Buffer

	code := Run([]string{"gate", "feedback", "the gate rejected a clean commit", "--upstream", "o/tool", "--dry", "--repo", repo},
		strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"title: the gate rejected a clean commit\n", "tracker: o/tool\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("--dry reached gh")
	}
}

func TestEscapeRecord_DryPrintsTheIssueAndRecordsNothing(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer

	code := runGate([]string{"escape", "record", "clippy passed and CI refused", "--kind", "false-positive", "--dry", "--repo", repo},
		strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"title: false-positive: clippy passed and CI refused\n", "labels: false-positive\n", "closes-by:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("--dry reached gh")
	}
	if _, err := os.Stat(tdd.EscapeLogPath()); err == nil {
		t.Error("--dry wrote an escape record")
	}
}

// A CI escape with no evidence of its own carries the reason as its evidence;
// one with evidence carries that instead.
func TestEscapeRecord_DryFromCIFillsBlankEvidenceWithTheReasonOnly(t *testing.T) {
	repo, _ := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer
	if code := runGate([]string{"escape", "record", "the lint job refused", "--from-ci", "lint", "--dry", "--repo", repo},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "**Evidence:** caught by lint\n\nthe lint job refused\n") {
		t.Errorf("blank evidence did not fall back to the reason:\n%s", out.String())
	}

	out.Reset()
	if code := runGate([]string{"escape", "record", "the lint job refused", "--from-ci", "lint", "--evidence", "error: x", "--dry", "--repo", repo},
		strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "**Evidence:** caught by lint\n\nerror: x\n") {
		t.Errorf("given evidence was not used:\n%s", out.String())
	}
}

func TestEscapeRecord_DryWithoutFromCIKeepsBlankEvidenceBlank(t *testing.T) {
	repo, _ := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer
	if code := runGate([]string{"escape", "record", "a miss", "--dry", "--repo", repo}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "**Evidence:** none recorded\n") {
		t.Errorf("a blank evidence was filled without --from-ci:\n%s", out.String())
	}
}

func TestEscapeRecord_DryRefusesWhatARecordRefuses(t *testing.T) {
	repo, _ := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer

	code := runGate([]string{"escape", "record", "x", "--kind", "bogus", "--dry", "--repo", repo}, strings.NewReader(""), &out, &errb)

	if code != 2 || !strings.Contains(errb.String(), "kind is") {
		t.Fatalf("exit %d, stderr %q; want the kind refusal", code, errb.String())
	}
}
