package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every test above this line in the package replaces runnerReportFn with a
// stub that answers directly, so fetchRunnerReport's own body — the two gh
// calls and the read that follows them — never runs. These tests call it
// directly, through the same fake-gh-on-PATH mechanism the escape tests use,
// so every ordinary reason "no measurement of this tree" can be true gets its
// own exact wording checked.

// artifactListNoun is the exact second argument fetchRunnerReport hands gh
// for the listing call, reconstructed here so a test can key the stub's
// per-call response to it (the query string's `?`/`&`/`=` are sanitized into
// the response env var's name by ghStubKeyPart, on both sides).
func artifactListNoun(tree string) string {
	return "repos/{owner}/{repo}/actions/artifacts?per_page=1&name=" + runnerArtifactName(tree)
}

// A gh that cannot even list the artifact is not "no measurement yet" — it is
// "the question could not be asked" — and the reason gh gave for that has to
// reach whoever reads the merge log.
func TestFetchRunnerReport_ListingFailsNamesTheReason(t *testing.T) {
	root := makeGitHubRepo(t)
	const tree = "cafef00d0000000000000000000000000000000"
	stubGhFail(t, "api "+artifactListNoun(tree), "gh: network error")

	report, absent := fetchRunnerReport(root, tree)

	if absent == "" {
		t.Fatal("absent = \"\", want a reason: the listing call failed")
	}
	if !strings.Contains(absent, "the published measurements could not be listed") {
		t.Errorf("absent = %q, want it to say the listing failed", absent)
	}
	if !strings.Contains(absent, "network error") {
		t.Errorf("absent = %q, want gh's own reason in it", absent)
	}
	if report.Tree != "" || report.Runner != "" || len(report.Mutants) != 0 {
		t.Errorf("report = %+v, want the zero value on a listing failure", report)
	}
}

// No artifact published for this tree yet is the ordinary, expected case —
// most trees this gate ever judges have no runner measurement at all — and it
// must name the artifact it looked for so the wording is checkable against
// what the CI job actually publishes.
func TestFetchRunnerReport_NoArtifactYetNamesTheArtifactItLookedFor(t *testing.T) {
	root := makeGitHubRepo(t)
	const tree = "deadbeef0000000000000000000000000000000"
	stubGh(t, "") // the artifacts lookup answers with nothing published

	report, absent := fetchRunnerReport(root, tree)

	want := "no run has published a measurement of this tree yet (no artifact " + runnerArtifactName(tree) + ")"
	if absent != want {
		t.Errorf("absent = %q, want %q", absent, want)
	}
	if report.Tree != "" || report.Runner != "" || len(report.Mutants) != 0 {
		t.Errorf("report = %+v, want the zero value when nothing was published", report)
	}
}

// A run id was found, but the download of that run's artifact failed — gh's
// own reason has to travel, and the run id it tried has to be in the message
// too, since that is the one a human re-runs `gh run download` against by
// hand.
func TestFetchRunnerReport_DownloadFailureNamesTheRunAndTheReason(t *testing.T) {
	root := makeGitHubRepo(t)
	const tree = "0000000000111111111122222222223333333333"
	stubGhScript(t, map[string]string{"api " + artifactListNoun(tree): "77"})
	stubGhFail(t, "run download", "gh: authentication required")

	report, absent := fetchRunnerReport(root, tree)

	if !strings.Contains(absent, "the measurement published by run 77 could not be downloaded") {
		t.Errorf("absent = %q, want the run id and \"could not be downloaded\"", absent)
	}
	if !strings.Contains(absent, "authentication required") {
		t.Errorf("absent = %q, want gh's own reason in it", absent)
	}
	if report.Tree != "" || report.Runner != "" || len(report.Mutants) != 0 {
		t.Errorf("report = %+v, want the zero value on a download failure", report)
	}
}

// A download that reports success but leaves nothing at the path the report
// is read from is exactly what an artifact with no file inside it, or a `gh`
// too old to understand `-D`, looks like — the read that follows has to say
// the measurement could not be read, not silently fabricate an empty one.
func TestFetchRunnerReport_NoFileAfterDownloadIsUnreadable(t *testing.T) {
	root := makeGitHubRepo(t)
	const tree = "44444444445555555555666666666677777777"
	stubGhScript(t, map[string]string{"api " + artifactListNoun(tree): "9"})
	// No response wired for "run download": the stub exits 0, prints
	// nothing, and — being a stub — writes no file, which is exactly the
	// case under test.

	report, absent := fetchRunnerReport(root, tree)

	if !strings.Contains(absent, "the downloaded measurement could not be read") {
		t.Errorf("absent = %q, want it to say the download left nothing to read", absent)
	}
	if report.Tree != "" || report.Runner != "" || len(report.Mutants) != 0 {
		t.Errorf("report = %+v, want the zero value when there is nothing to parse", report)
	}
}

// readRunnerReport is the read fetchRunnerReport ends in. Text that is not
// JSON at all is exactly what a truncated or corrupted download leaves behind
// — nothing is guessed out of it.
func TestReadRunnerReport_TextThatIsNotJSONIsUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), runnerReportFile)
	if err := os.WriteFile(path, []byte("not a json report"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, absent := readRunnerReport(path)

	if !strings.Contains(absent, "the downloaded measurement could not be read") {
		t.Errorf("absent = %q, want it to say the report could not be read", absent)
	}
	if report.Tree != "" || report.Runner != "" || len(report.Mutants) != 0 {
		t.Errorf("report = %+v, want the zero value on a parse failure", report)
	}
}

// The happy path: a well-formed report downloads and parses into exactly the
// values it was written with. This is the case every other test in this file
// exists to be distinguished from.
func TestReadRunnerReport_ParsesAWellFormedReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), runnerReportFile)
	const body = `{
  "tree": "abc123",
  "runner": "self-hosted-linux-1 (linux)",
  "base": "origin/main",
  "mutants": [
    {"file": "calc.go", "line": 3, "col": 39, "mutation": "CONDITIONALS_BOUNDARY", "status": "caught"}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	report, absent := readRunnerReport(path)

	if absent != "" {
		t.Fatalf("absent = %q, want a well-formed report to read cleanly", absent)
	}
	if report.Tree != "abc123" || report.Runner != "self-hosted-linux-1 (linux)" || report.Base != "origin/main" {
		t.Errorf("report = %+v, want the tree/runner/base read out of the file", report)
	}
	if len(report.Mutants) != 1 {
		t.Fatalf("mutants = %+v, want the one mutant the file names", report.Mutants)
	}
	m := report.Mutants[0]
	if m.File != "calc.go" || m.Line != 3 || m.Col != 39 || m.Mutation != "CONDITIONALS_BOUNDARY" || m.Status != "caught" {
		t.Errorf("mutant = %+v, want it to match the file verbatim", m)
	}
}
