package tdd

import (
	"os"
	"strings"
	"testing"
)

// The report is gremlins' own, captured from a real run rather than written
// by hand: a parser tested against invented bytes is a parser tested against
// nothing.
func gremlinsReport(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/gremlins_report.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Every status is its own outcome, and the counts come from the mutant list
// rather than from the report's own totals — gremlins wrote 0 for every total
// in the captured run while listing three mutants.
func TestParseGremlinsReport_CountsFromTheMutantListNotTheTotals(t *testing.T) {
	r, err := parseGremlinsReport(gremlinsReport(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 3 {
		t.Fatalf("read %d mutants, want the 3 the report lists", len(r))
	}
	for _, m := range r {
		if m.File != "calc.go" || m.Line == 0 || m.Mutation == "" {
			t.Fatalf("mutant %+v is missing what identifies it", m)
		}
		if m.Status != "timeout" {
			t.Fatalf("status = %q, want gremlins' TIMED OUT read as a timeout", m.Status)
		}
	}
}

// The four statuses that decide a merge, mapped once: what was caught, what
// survived, what timed out (an unmeasured mutant, not a result), and what
// could not compile.
func TestGremlinsStatus_MapsEveryVerdictItCanReport(t *testing.T) {
	for raw, want := range map[string]string{
		"KILLED":      "caught",
		"LIVED":       "missed",
		"NOT COVERED": "missed",
		"TIMED OUT":   "timeout",
		"NOT VIABLE":  "unviable",
		"RUNERROR":    "unviable",
	} {
		if got := gremlinsStatus(raw); got != want {
			t.Errorf("gremlinsStatus(%q) = %q, want %q", raw, got, want)
		}
	}
	// A status this binary has never heard of is NOT quietly a pass.
	if got := gremlinsStatus("SOMETHING NEW"); got == "caught" {
		t.Errorf("an unknown status read as caught: %q", got)
	}
}

// A survivor somebody signed off on, with a reason, is not an unaccepted one.
// A survivor with no entry is the whole rule.
func TestGoMutantsReceipt_AcceptsOnlyTheSurvivorsWithAReason(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "calc.go:4 CONDITIONALS_BOUNDARY # the bound is the sweep's own age bar, pinned by the sweep test",`,
		`  "calc.go:5 ARITHMETIC_BASE",`,
		"]",
	}, "\n"))

	survivors := []MutantOutcome{
		{File: "calc.go", Line: 4, Mutation: "CONDITIONALS_BOUNDARY", Status: "missed"},
		{File: "calc.go", Line: 5, Mutation: "ARITHMETIC_BASE", Status: "missed"},
		{File: "calc.go", Line: 9, Mutation: "CONDITIONALS_NEGATION", Status: "missed"},
	}
	accepted, unaccepted := splitAcceptedSurvivors(root, survivors)
	if len(accepted) != 1 || accepted[0].Line != 4 {
		t.Fatalf("accepted = %+v, want only the entry that states a reason", accepted)
	}
	if len(unaccepted) != 2 {
		t.Fatalf("unaccepted = %+v, want the unlisted survivor AND the one with no reason", unaccepted)
	}
}

// The run is scoped to the lane's diff, writes machine-readable output, and
// is capped: gremlins re-runs the suite per mutant, so an uncapped run owns
// the box for as long as it takes.
func TestGremlinsArgv_ScopesToTheLaneDiffAndCapsItself(t *testing.T) {
	got := strings.Join(gremlinsArgv("abc123", "out.json", 2), " ")
	for _, want := range []string{"unleash", "--diff abc123", "--output out.json", "--workers 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("gremlinsArgv = %q, want it to carry %q", got, want)
		}
	}
}

// The receipt a Go run writes is the same document a Rust run writes: the
// merge gate reads one shape, whatever measured it.
func TestGoMutantsReceipt_IsTheSameReceiptTheRustRunnerWrites(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	j := MutantsJob{Repo: "borld", RepoRoot: root, Worktree: root, TipTree: laneTip,
		BaseSHA: mergeBase, Branch: "lane/x", TargetDir: root}

	mutants, err := parseGremlinsReport(gremlinsReport(t))
	if err != nil {
		t.Fatal(err)
	}
	writeGoMutantsReceipt(j, mutants, TreeState{})

	r := readReceipt(t, laneTip)
	if r.Repo != "borld" || r.TipTree != laneTip || r.BaseSHA != mergeBase {
		t.Fatalf("receipt = %+v, want it to name the run it describes", r)
	}
	if r.MutantsTotal != 3 || r.Timeout != 3 {
		t.Fatalf("receipt counts = total %d timeout %d, want 3 and 3", r.MutantsTotal, r.Timeout)
	}
	if r.Verdict != "pass" {
		t.Fatalf("verdict = %q, want pass — timeouts are refused by the merge gate, not by the runner", r.Verdict)
	}
	if r.MAC == "" {
		t.Fatal("a receipt the runner wrote must be signed")
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip}); got == nil || !got.Blocked {
		t.Fatal("three timed-out mutants must not merge")
	}
}
