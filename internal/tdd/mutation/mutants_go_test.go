package mutation

import (
	"os"
	"path/filepath"
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

// gremlins writes EVERY mutant it analysed into the report, including the ones
// its own --diff scope skipped: 4404 SKIPPED against 21 measured, on this
// repo's own lane diff (2026-09-03). A skipped mutant was not measured and is
// not in scope; read as "unviable" it made the receipt a merge reads claim
// 4425 mutants for a run that judged 21.
func TestParseGremlinsReport_LeavesOutTheMutantsTheDiffScopeSkipped(t *testing.T) {
	got, err := parseGremlinsReport([]byte(`{"files":[{"file_name":"calc.go","mutations":[
		{"type":"CONDITIONALS_BOUNDARY","status":"SKIPPED","line":4,"column":5},
		{"type":"ARITHMETIC_BASE","status":"KILLED","line":9,"column":2}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d mutants, want only the one the run measured: %+v", len(got), got)
	}
	if got[0].Status != "caught" {
		t.Fatalf("status = %q, want the measured mutant's own", got[0].Status)
	}
}

// The four statuses that decide a merge, mapped once: what was caught, what
// survived, what timed out (an unmeasured mutant, not a result), and what
// could not compile.
func TestGremlinsStatus_MapsEveryVerdictItCanReport(t *testing.T) {
	for raw, want := range map[string]string{
		"KILLED": "caught",
		"LIVED":  "missed",
		// NOT COVERED is its own status, not a miss: gremlins never ran a test
		// for it, so it is unmeasured rather than survived. See
		// gremlinsNotCovered for why its coverage mapping cannot be read as
		// "no test covers this".
		"NOT COVERED": gremlinsNotCovered,
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
// ratchet: test_removed TestGoMutantsReceipt_AcceptsOnlyTheSurvivorsWithAReason: renamed for the function it actually calls, splitAcceptedSurvivors; the assertions are unchanged
func TestSplitAcceptedSurvivors_AcceptsOnlyTheSurvivorsWithAReason(t *testing.T) {
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
	list, _ := acceptedMutants(root)
	accepted, unaccepted, _, _ := splitAcceptedSurvivors(list, survivors)
	if len(accepted) != 1 || accepted[0].Line != 4 {
		t.Fatalf("accepted = %+v, want only the entry that states a reason", accepted)
	}
	if len(unaccepted) != 2 {
		t.Fatalf("unaccepted = %+v, want the unlisted survivor AND the one with no reason", unaccepted)
	}
}

// The contract issue #339 depends on: accepted counts mutants THIS RUN
// measured and accepted, never the accept-list's own size. An accept-list can
// carry entries for mutants long since fixed or renamed — normal debt — and a
// run that measures nothing must report zero accepted regardless of how many
// entries the list carries.
// ratchet: test_removed TestGoMutantsReceipt_AnAcceptListNamingUnmeasuredEntriesDoesNotInflateAccepted: renamed and re-pointed at judgeMutants, which is what counts a measurement now; the claim is unchanged
func TestJudgeMutants_AnAcceptListNamingUnmeasuredEntriesDoesNotInflateAccepted(t *testing.T) {
	t.Parallel()
	cfg := MutantsConfig{Accept: []string{
		"calc.go:1 CONDITIONALS_BOUNDARY # kind=equivalent: accepted on an earlier run",
		"calc.go:2 ARITHMETIC_BASE # kind=equivalent: accepted on an earlier run",
		"calc.go:3 CONDITIONALS_NEGATION # kind=equivalent: accepted on an earlier run",
	}}

	// This run measured nothing: no survivors, no timeouts, nothing at all.
	v := judgeMutants(cfg, nil)

	if v.Tested != 0 {
		t.Fatalf("Tested = %d, want 0 — nothing was measured", v.Tested)
	}
	if v.Accepted != 0 {
		t.Fatalf("Accepted = %d, want 0 — the accept list's own size must never stand in for what this run measured and accepted", v.Accepted)
	}
}

// A `]` INSIDE a reason's own quoted text (describing bracket-indexing code,
// say) is not the array's closing bracket. tomlStringsIn used to close the
// whole array on the first line containing any `]`, quoted or not, which
// silently dropped every accept-list entry after the one that happened to
// mention a bracket (issue #139).
func TestTomlStringsIn_AQuotedBracketDoesNotCloseTheArrayEarly(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "calc.go:1 CONDITIONALS_BOUNDARY # first entry, no bracket",`,
		`  "calc.go:2 ARITHMETIC_BASE # start indexes '[' and end indexes ']' in the same string",`,
		`  "calc.go:3 CONDITIONALS_NEGATION # third entry, must still be read",`,
		"]",
	}, "\n"))

	survivors := []MutantOutcome{
		{File: "calc.go", Line: 1, Mutation: "CONDITIONALS_BOUNDARY", Status: "missed"},
		{File: "calc.go", Line: 2, Mutation: "ARITHMETIC_BASE", Status: "missed"},
		{File: "calc.go", Line: 3, Mutation: "CONDITIONALS_NEGATION", Status: "missed"},
	}
	list, _ := acceptedMutants(root)
	accepted, unaccepted, _, _ := splitAcceptedSurvivors(list, survivors)
	if len(accepted) != 3 {
		t.Fatalf("accepted = %+v, unaccepted = %+v, want all three entries read past the one with a bracket in its own reason", accepted, unaccepted)
	}
}

// An EMPTY quoted string ("") closes on the character immediately after the
// one that opened it: stripQuoted must still treat what follows as real
// syntax, not swallow it the way an unterminated quote correctly does.
func TestStripQuoted_AnEmptyQuotedStringStillLeavesWhatFollows(t *testing.T) {
	if got := stripQuoted(`""]`); got != "]" {
		t.Fatalf("stripQuoted(%q) = %q, want %q — an empty quoted pair strips to nothing, leaving the real ]", `""]`, got, "]")
	}
}

// The run is scoped to the lane's diff, writes machine-readable output, and
// is capped: gremlins re-runs the suite per mutant, so an uncapped run owns
// the box for as long as it takes.
func TestGremlinsArgv_ScopesToTheLaneDiffAndCapsItself(t *testing.T) {
	got := strings.Join(gremlinsArgv("abc123", "out.json", 2, nil), " ")
	for _, want := range []string{"unleash", "--diff abc123", "--output out.json", "--workers 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("gremlinsArgv = %q, want it to carry %q", got, want)
		}
	}
}

// gremlins gathers coverage per package unless told otherwise, so code in a
// low internal/tdd package that is exercised only from a higher package's
// tests (or from internal/cli) reads NOT COVERED and is never judged. The
// whole module is the coverage scope: the narrower ./internal/tdd/... form
// measured 112 covered / 22 NOT COVERED on the split's sample diff where
// ./... measured 126 / 8.
func TestGremlinsArgv_GathersCoverageAcrossTheWholeModule(t *testing.T) {
	got := strings.Join(gremlinsArgv("abc123", "out.json", 1, nil), " ")
	if !strings.Contains(got, "--coverpkg ./...") {
		t.Fatalf("gremlinsArgv = %q, want it to carry %q", got, "--coverpkg ./...")
	}
}

// #704: the self-hosted runner's coverage gather dies at Go's default
// 10-minute test timeout before gremlins ever judges a mutant, and --silent
// was the reason nobody could see that: it swallows the log.Infof that
// carries the failing `go test`'s own output, leaving only gremlins' one-line
// wrapper error. A silent run is the wrong default while the gather is
// broken, so the flag must not be present until #704 is closed.
func TestGremlinsArgv_DoesNotRunSilentWhileTheCoverageGatherIsBroken(t *testing.T) {
	got := gremlinsArgv("abc123", "out.json", 1, nil)
	for _, arg := range got {
		if arg == "--silent" {
			t.Fatalf("gremlinsArgv = %q, must not carry --silent while #704's coverage gather fails silently", got)
		}
	}
}

// gremlins takes a PATH, not a Go package pattern. Handed "./..." it walks
// nothing, prints "No results to report" and exits 0 — a mutation gate that
// always passes, which is the one failure mode this design cannot have.
// Measured over this repo's own lane diff on 2026-09-03: "./..." found 0
// mutants where "." found 20 runnable.
func TestGremlinsArgv_PassesTheModuleRootNotAPackagePattern(t *testing.T) {
	got := gremlinsArgv("abc123", "out.json", 1, nil)
	if last := got[len(got)-1]; last != "." {
		t.Fatalf("gremlins path = %q, want the module root \".\" — a package pattern makes it walk nothing and pass", last)
	}
}

// gremlins takes exactly one positional path (cobra.MaximumNArgs(1)), so a
// file this run already has a valid measurement for is narrowed out through
// its OWN `--exclude-files <regexp>` flag instead — anchored and escaped, so
// a file whose path happens to be a substring of another's is never excluded
// by accident (issue #143).
func TestGremlinsArgv_ExcludesAlreadyMeasuredFilesByAnchoredRegexp(t *testing.T) {
	got := strings.Join(gremlinsArgv("abc123", "out.json", 1, []string{"internal/tdd/receipt.go", "a.b.go"}), " ")
	for _, want := range []string{
		"--exclude-files ^internal/tdd/receipt\\.go$",
		"--exclude-files ^a\\.b\\.go$",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("gremlinsArgv = %q, want it to carry %q", got, want)
		}
	}
}

// ratchet: test_removed TestGoMutantsReceipt_IsTheSameReceiptTheRustRunnerWrites: there is no receipt; both runners now report into one Verdict, and MeasureLane's own tests judge a gremlins report through the same finishMeasure a Cargo run uses

// A reason that quotes code writes its quotes escaped, `\"`, as TOML requires.
// tomlStringsIn ended the string on the escaped quote itself, so this repo's
// own accept-list entry quoting `if profileWs == \"\"` came back in pieces,
// one piece was refused as malformed, and the refusal made the whole list
// unreadable: every Go measurement ended "the accept-list could not be read"
// with no survivor at all (#704).
func TestTomlStringsIn_AnEscapedQuoteStaysInsideItsString(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "calc.go:1 CONDITIONALS_NEGATION # the \"cargo\" guard",`,
		`  "calc.go:2 CONDITIONALS_NEGATION # the if x == \"\" fallback, a \\ too",`,
		"]",
	}, "\n"))

	got := tomlStringsIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", "mutation-accept")
	want := []string{
		`calc.go:1 CONDITIONALS_NEGATION # the "cargo" guard`,
		`calc.go:2 CONDITIONALS_NEGATION # the if x == "" fallback, a \ too`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tomlStringsIn = %q, want %q", got, want)
	}
}
