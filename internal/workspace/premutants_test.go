package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A PR opens only after the lane's diff has been measured the way CI's
// mutants-verdict job will measure it, when the repo declares
// mutants-before-pr. These drive the real measurement (tdd.MeasureLane) with
// the mutation tool itself replaced through its existing exec seam: no test
// here runs cargo-mutants or gremlins.

// isolateMeasurement points every box-wide thing the measurement touches at
// this test: its lock directory, its gate state, the CI-runner probe, the
// shard count and the drive budget.
func isolateMeasurement(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lockDir := t.TempDir()
	t.Cleanup(tdd.SetLockDirForTest(lockDir))
	t.Cleanup(tdd.SetCIRunnerJobsForTest(func() []int { return nil }))
	t.Cleanup(tdd.SetMutantsShardsForTest(1))
	t.Cleanup(tdd.SetFreeSpaceForTest(999, true))
	return lockDir
}

// gitIn runs one git command in repo, failing the test if git does.
func gitIn(t *testing.T, repo string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// writeRel writes one repo file, making its directory first.
func writeRel(t *testing.T, repo, rel, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cargoLane builds a one-crate cargo workspace on main, pushed to origin,
// then a lane branch `lane` that changes the crate's one source line, also
// pushed. beforePR declares mutants-before-pr = true in the workspace's own
// metadata table.
func cargoLane(t *testing.T, beforePR bool) string {
	t.Helper()
	repo := repoWithRemote(t)
	manifest := "[workspace]\nmembers = [\"crates/a\"]\n"
	if beforePR {
		manifest += "\n[workspace.metadata.aphrollo]\nmutants-before-pr = true\n"
	}
	writeRel(t, repo, "Cargo.toml", manifest)
	writeRel(t, repo, "crates/a/Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n")
	writeRel(t, repo, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b }\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "trunk")
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "checkout", "-q", "-b", "lane")
	writeRel(t, repo, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b + 0 }\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "lane")
	gitIn(t, repo, "push", "-q", "-u", "origin", "lane")
	return repo
}

// cargoOutcomes is a cargo-mutants outcomes.json naming one mutant on the
// lane's changed line with the given summary.
func cargoOutcomes(summary string) string {
	return `{"outcomes":[
  {"scenario":"Baseline","summary":"Success"},
  {"scenario":{"Mutant":{"name":"crates/a/src/lib.rs:1:36: replace + with -","package":"a","file":"crates/a/src/lib.rs","span":{"start":{"line":1,"column":36}}}},"summary":"` + summary + `"}
]}`
}

// fakeMutants stands in for cargo-mutants: every measuring call writes the
// given outcomes where the real tool would, and records the diff it was
// scoped to. The `--list` probe answers nothing, which the run reads as "do
// not cap the shard count".
type fakeMutants struct {
	calls int
	diffs []string
}

func stubMutants(t *testing.T, summary string) *fakeMutants {
	t.Helper()
	f := &fakeMutants{}
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, _ string, _, argv []string, _ io.Writer) (int, error) {
		if slices.Contains(argv, "--list") {
			return 1, nil
		}
		f.calls++
		if i := slices.Index(argv, "--in-diff"); i >= 0 && i+1 < len(argv) {
			data, err := os.ReadFile(argv[i+1])
			if err != nil {
				t.Errorf("reading the diff the run was scoped to: %v", err)
			}
			f.diffs = append(f.diffs, string(data))
		}
		i := slices.Index(argv, "--output")
		if i < 0 || i+1 >= len(argv) {
			t.Errorf("argv carries no --output: %v", argv)
			return 1, nil
		}
		writeRel(t, argv[i+1], "mutants.out/outcomes.json", cargoOutcomes(summary))
		if summary == "CaughtMutant" {
			return 0, nil
		}
		return 2, nil
	}))
	return f
}

// noPRYet stubs gh so no PR exists for the branch and records a create.
func noPRYet(t *testing.T) *bool {
	t.Helper()
	created := false
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			created = true
			return &PRInfo{Number: 7, URL: "https://github.com/o/r/pull/7", State: "OPEN", IsDraft: req.Draft}, nil
		},
	)
	return &created
}

func TestPR_AnUnacceptedSurvivorStopsThePRNamingIt(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = pr.Apply(&out, &errb)

	if err == nil {
		t.Fatalf("Apply opened the PR over an unaccepted survivor\nstdout: %s", out.String())
	}
	if *created {
		t.Fatal("gh pr create ran although the measurement refused")
	}
	if !strings.Contains(out.String(), "\ncrates/a/src/lib.rs:1:36 replace + with - (survived)\n") {
		t.Errorf("the survivor is not named as file:line:col MUTATOR (survived):\n%s", out.String())
	}
	if !strings.Contains(out.String(), "mutation-accept") {
		t.Errorf("no remedy line naming mutation-accept:\n%s", out.String())
	}
}

func TestPR_AMutantThatTimedOutTwiceStopsThePRNamingIt(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	stubMutants(t, "Timeout")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = pr.Apply(&out, &errb)

	if err == nil || *created {
		t.Fatalf("Apply opened the PR over a mutant that timed out twice (err=%v)\nstdout: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "\ncrates/a/src/lib.rs:1:36 replace + with - (timed out)\n") {
		t.Errorf("the timeout is not named as file:line:col MUTATOR (timed out):\n%s", out.String())
	}
}

func TestPR_ACaughtMutantOpensThePR(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	fake := stubMutants(t, "CaughtMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
	}
	if fake.calls == 0 {
		t.Fatal("the lane was never measured")
	}
	if !*created {
		t.Fatal("a lane whose only mutant was caught did not get its PR")
	}
}

func TestPR_ADriveTooFullToMeasureOpensThePRDeferringToCI(t *testing.T) {
	isolateMeasurement(t)
	t.Cleanup(tdd.SetFreeSpaceForTest(0, true))
	repo := cargoLane(t, true)
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply refused a PR the box merely could not measure: %v\nstdout: %s", err, out.String())
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
	if fake.calls != 0 {
		t.Errorf("the tool ran %d time(s) on a drive the budget refused", fake.calls)
	}
	if !strings.Contains(out.String(), "deferred to CI") {
		t.Errorf("no note that the measurement was deferred to CI:\n%s", out.String())
	}
}

func TestPR_AHeldMutationRunLockOpensThePRDeferringToCI(t *testing.T) {
	lockDir := isolateMeasurement(t)
	// The holder record of the box-wide run lock, naming this (live)
	// process: what another measurement in progress leaves beside the lock.
	tdd.WriteFileLockOwner(filepath.Join(lockDir, "aphrollo-mutants-run.lock.owner"), "mutants measure for /elsewhere", "/elsewhere")
	repo := cargoLane(t, true)
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply refused a PR while another measurement held the lock: %v\nstdout: %s", err, out.String())
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
	if fake.calls != 0 {
		t.Errorf("the tool ran %d time(s) while another run held the lock", fake.calls)
	}
	if !strings.Contains(out.String(), "deferred to CI") {
		t.Errorf("no note that the measurement was deferred to CI:\n%s", out.String())
	}
}

func TestPR_SkipMutantsWithoutAReasonIsRefused(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	fake := stubMutants(t, "CaughtMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	pr.Skip = SkipMutants{Set: true, Reason: "  "}
	var out, errb bytes.Buffer
	err = pr.Apply(&out, &errb)

	if err == nil || !strings.Contains(err.Error(), "--skip-mutants") {
		t.Fatalf("a reasonless --skip-mutants was not refused by name (err=%v)", err)
	}
	if *created || fake.calls != 0 {
		t.Errorf("created=%v, measured %d time(s): a refused override must do neither", *created, fake.calls)
	}
}

func TestPR_SkipMutantsWithAReasonOpensThePRUnmeasured(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	pr.Skip = SkipMutants{Set: true, Reason: "survivor accepted in review"}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s", err, out.String())
	}
	if !*created {
		t.Fatal("the override did not open the PR")
	}
	if fake.calls != 0 {
		t.Errorf("the lane was measured %d time(s) under --skip-mutants", fake.calls)
	}
	if !strings.Contains(out.String(), "survivor accepted in review") {
		t.Errorf("the skip does not say its reason:\n%s", out.String())
	}
}

func TestPR_TheMeasuredDiffExcludesLinesMergedInFromMain(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	// main moves on after the lane forked, and the lane catches up by
	// merging it: main's new file is in the lane's tree but is not the
	// lane's change.
	gitIn(t, repo, "checkout", "-q", "main")
	writeRel(t, repo, "crates/a/src/extra.rs", "pub fn mul(a: i32, b: i32) -> i32 { a * b }\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "main moves on")
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "checkout", "-q", "lane")
	gitIn(t, repo, "merge", "-q", "--no-edit", "origin/main")
	gitIn(t, repo, "push", "-q", "origin", "lane")
	fake := stubMutants(t, "CaughtMutant")
	noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
	}
	if len(fake.diffs) == 0 {
		t.Fatal("the lane was never measured")
	}
	for _, d := range fake.diffs {
		if !strings.Contains(d, "crates/a/src/lib.rs") {
			t.Errorf("the lane's own change is missing from the measured diff:\n%s", d)
		}
		if strings.Contains(d, "extra.rs") {
			t.Errorf("the measured diff carries main's merged-in file:\n%s", d)
		}
	}
}

func TestPR_KeyOffMeansNoMeasurement(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, false)
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s", err, out.String())
	}
	if fake.calls != 0 {
		t.Errorf("a repo without mutants-before-pr was measured %d time(s)", fake.calls)
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
}

func TestSubmit_AnUnacceptedSurvivorStopsThePR(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	stubMutants(t, "MissedMutant")
	created := noPRYet(t)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "none"}, nil })
	stubReady(t, func(wt, branch string) error { return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })

	s, err := SubmitPlan(targetFor(repo, "lane"), "summary")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = s.Apply(&out, &errb)

	if err == nil || *created {
		t.Fatalf("submit opened the PR over an unaccepted survivor (err=%v)\nstdout: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "crates/a/src/lib.rs:1:36 replace + with - (survived)") {
		t.Errorf("the survivor is not named:\n%s", out.String())
	}
}

func TestShipPlan_SkipMutantsWithoutAReasonIsRefusedBeforeAnythingRuns(t *testing.T) {
	repo := cargoLane(t, true)
	writeRel(t, repo, "notes.txt", "uncommitted\n")

	_, err := ShipPlan(targetFor(repo, "lane"), ShipRequest{
		Message:     "work",
		StageAll:    true,
		SkipMutants: SkipMutants{Set: true, Reason: ""},
	})

	if err == nil || !strings.Contains(err.Error(), "--skip-mutants") {
		t.Fatalf("ShipPlan accepted a reasonless --skip-mutants (err=%v)", err)
	}
}

func TestPR_ARunThatReachedNoVerdictStopsThePRWithItsReport(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	// The tool exits without writing any outcomes: no verdict at all.
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, _ string, _, _ []string, _ io.Writer) (int, error) {
		return 1, nil
	}))
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = pr.Apply(&out, &errb)

	if err == nil || *created {
		t.Fatalf("Apply opened the PR over a run that reached no verdict (err=%v)\nstdout: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "reached no verdict") {
		t.Errorf("the refusal does not carry the run's own report:\n%s", out.String())
	}
}

func TestPR_ARunnerThatCannotStartOpensThePRDeferringToCI(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, _ string, _, _ []string, _ io.Writer) (int, error) {
		return 0, errors.New("cargo-mutants: executable file not found in $PATH")
	}))
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply refused a PR because the tool could not start here: %v\nstdout: %s", err, out.String())
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
	if !strings.Contains(out.String(), "deferred to CI") || !strings.Contains(out.String(), "executable file not found") {
		t.Errorf("no deferral note naming why the run could not start:\n%s", out.String())
	}
}

func TestPR_NoMergeBaseWithTheBaseOpensThePRDeferringToCI(t *testing.T) {
	isolateMeasurement(t)
	repo := cargoLane(t, true)
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "release-that-does-not-exist", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s", err, out.String())
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
	if fake.calls != 0 {
		t.Errorf("the tool ran %d time(s) with no base to scope it to", fake.calls)
	}
	if !strings.Contains(out.String(), "deferred to CI") {
		t.Errorf("no note that the measurement was deferred to CI:\n%s", out.String())
	}
}

func TestPR_AToolThatCannotMeasureOnThisPlatformOpensThePRDeferringToCI(t *testing.T) {
	isolateMeasurement(t)
	// gremlins reads 0.00% coverage on Windows, so a Go lane there is not
	// measured at all (the Windows stand-down).
	t.Cleanup(tdd.SetMutantsGOOSForTest("windows"))
	repo := repoWithRemote(t)
	writeRel(t, repo, "go.mod", "module example.com/m\n\ngo 1.22\n")
	writeRel(t, repo, "aphrollo.toml", "[aphrollo]\nmutants-before-pr = true\n")
	writeRel(t, repo, "m.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "trunk")
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "checkout", "-q", "-b", "lane")
	writeRel(t, repo, "m.go", "package m\n\nfunc Add(a, b int) int { return a + b + 0 }\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "lane")
	gitIn(t, repo, "push", "-q", "-u", "origin", "lane")
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, _ string, _, argv []string, _ io.Writer) (int, error) {
		t.Errorf("the tool ran on a platform that stands the measurement down: %v", argv)
		return 1, nil
	}))
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout: %s", err, out.String())
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
	if !strings.Contains(out.String(), "deferred to CI") {
		t.Errorf("no note that the measurement was deferred to CI:\n%s", out.String())
	}
}

func TestPR_ABusyCIRunnerOpensThePRDeferringToCI(t *testing.T) {
	isolateMeasurement(t)
	// A runner job is busy when the PR is about to open and finishes right
	// after: the merge gate would wait it out and measure, the PR must not.
	probes := 0
	t.Cleanup(tdd.SetCIRunnerJobsForTest(func() []int {
		probes++
		if probes == 1 {
			return []int{4242}
		}
		return nil
	}))
	repo := cargoLane(t, true)
	fake := stubMutants(t, "MissedMutant")
	created := noPRYet(t)

	pr, err := PRPlan(targetFor(repo, "lane"), "", "title", "body", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := pr.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply waited out a busy CI runner and refused: %v\nstdout: %s", err, out.String())
	}
	if !*created {
		t.Fatal("the PR was not opened")
	}
	if fake.calls != 0 {
		t.Errorf("the tool ran %d time(s) after a busy CI runner was seen", fake.calls)
	}
	if !strings.Contains(out.String(), "deferred to CI") || !strings.Contains(out.String(), "CI runner") {
		t.Errorf("no deferral note naming the busy CI runner:\n%s", out.String())
	}
}
