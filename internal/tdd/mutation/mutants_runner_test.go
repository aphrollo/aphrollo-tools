package mutation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A measurement made on another box is evidence about the tree THAT box
// measured, and about no other tree. The pre-merge gate judges a merge it
// builds locally — trunk checked out in a throwaway worktree with the lane
// merged into it — so what it holds is a tree, not a branch name and not a
// commit the runner has ever seen. Binding a runner's verdict to it by
// anything softer than the tree's own identity is how a gate ends up
// trusting a measurement of different code.
//
// The identity is git's: the tree object the merged index writes out. Two
// trees with the same id are the same bytes, whatever commit, branch, box or
// line ending produced them; two trees with different ids are different code
// and the verdict about one says nothing about the other. A mismatch is
// therefore not weak evidence to be discounted — it is no evidence, and the
// gate reports it exactly as it reports a measurement that never happened
// (issue #697).
func TestMeasure_ARunnerVerdictMeasuredOnADifferentTreeIsNotConsumed(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(SetMutantsGOOSForTest("windows"))
	// A clean bill of health — every mutant caught — for a tree that is not
	// the one in front of the gate. This is the dangerous direction: consumed,
	// it would pass this merge on somebody else's green.
	const otherTree = "c0ffee1111111111111111111111111111111111"
	t.Cleanup(setRunnerReportForTest(func(string, string) (RunnerReport, string) {
		return RunnerReport{Tree: otherTree, Runner: "self-hosted linux", Mutants: []MutantOutcome{
			{File: "calc.go", Line: 3, Col: 39, Mutation: "CONDITIONALS_BOUNDARY", Status: "caught"},
		}}, ""
	}))
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Tested != 0 || v.Caught != 0 {
		t.Errorf("verdict = %+v, want nothing adopted: the runner measured another tree", v)
	}
	if v.NotMeasured == "" {
		t.Errorf("NotMeasured = %q, want the reason this merge still carries no mutation evidence", v.NotMeasured)
	}
	if v.Refused {
		t.Errorf("a verdict about another tree must not block this merge either, got %+v", v)
	}
	for _, want := range []string{"NOT MEASURED", "NO mutation evidence", otherTree, mustTreeID(t, root)} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("merge-time output never says %q:\n%s", want, log.String())
		}
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-unmeasured:runner-tree-mismatch") {
		t.Errorf("gate.log files the mismatch as something else:\n%s", gateLogText(t, cfgDir))
	}
}

// A tree git cannot write out has no identity, and a merge with no identity
// cannot be shown to be the thing anybody measured. The runner is not even
// asked: there is nothing to ask it FOR, and "could not identify" must never
// quietly become "matches" — the same rule refuseIfGitFailed already applies
// one step further on, where a tree nobody could read refuses rather than
// passes.
func TestMeasure_ATreeGitCannotIdentifyConsumesNoRunnerVerdictAtAll(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(SetMutantsGOOSForTest("windows"))
	t.Cleanup(setGitDiffOutForTest(func(string, ...string) (string, string, error) {
		return "", "error: Entry 'calc.go' not uptodate. Cannot merge.", errors.New("exit status 128")
	}))
	asked := false
	t.Cleanup(setRunnerReportForTest(func(string, string) (RunnerReport, string) {
		asked = true
		return RunnerReport{}, "never reached"
	}))
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if asked {
		t.Errorf("asked the runner for a measurement of a tree this gate cannot name")
	}
	if v.Tested != 0 || v.NotMeasured == "" || v.Refused {
		t.Errorf("verdict = %+v, want nothing adopted and the merge left inconclusive", v)
	}
	if !strings.Contains(log.String(), "not uptodate") {
		t.Errorf("merge-time output never says what git said:\n%s", log.String())
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-unmeasured:no-tree-identity") {
		t.Errorf("gate.log files it as something else:\n%s", gateLogText(t, cfgDir))
	}
}

// A report naming no tree at all is the same refusal with a different reason,
// and the reason is worth its own line: "it measured tree <blank>" tells a
// reader that something went wrong at the far end, while the mismatch wording
// would have them hunting for a tree that was never written down.
func TestMeasure_ARunnerVerdictNamingNoTreeIsNotConsumed(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(SetMutantsGOOSForTest("windows"))
	t.Cleanup(setRunnerReportForTest(func(string, string) (RunnerReport, string) {
		return RunnerReport{Runner: "self-hosted linux", Mutants: []MutantOutcome{
			{File: "calc.go", Line: 3, Col: 39, Mutation: "CONDITIONALS_BOUNDARY", Status: "caught"},
		}}, ""
	}))
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Tested != 0 || v.NotMeasured == "" || v.Refused {
		t.Errorf("verdict = %+v, want nothing adopted and the merge left inconclusive", v)
	}
	if !strings.Contains(log.String(), "names no tree at all") {
		t.Errorf("merge-time output never says the report bound itself to nothing:\n%s", log.String())
	}
}

// And the other half of the same rule: a verdict measured on THIS tree is
// this tree's own measurement. The accept-list, the sources and the tests the
// runner judged are in the tree the id names, so its outcomes are judged here
// by the same finishMeasure a local run's are — one judge, whichever box
// produced the outcomes.
func TestMeasure_ARunnerVerdictMeasuredOnThisTreeIsConsumed(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	tree := mustTreeID(t, root)
	t.Cleanup(SetMutantsGOOSForTest("windows"))
	t.Cleanup(setRunnerReportForTest(func(string, string) (RunnerReport, string) {
		return RunnerReport{Tree: tree, Runner: "self-hosted linux", Mutants: []MutantOutcome{
			{File: "calc.go", Line: 3, Col: 39, Mutation: "CONDITIONALS_BOUNDARY", Status: "caught"},
		}}, ""
	}))
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.NotMeasured != "" {
		t.Errorf("NotMeasured = %q — this tree WAS measured, on the runner", v.NotMeasured)
	}
	if v.Tested != 1 || v.Caught != 1 {
		t.Errorf("verdict = %+v, want the runner's one caught mutant adopted", v)
	}
	if v.Refused {
		t.Errorf("nothing survived, so nothing is refused: %+v", v)
	}
	if !strings.Contains(log.String(), "self-hosted linux") || !strings.Contains(log.String(), tree) {
		t.Errorf("merge-time output never says where the measurement came from or which tree it was made on:\n%s",
			log.String())
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-passed:tested=1,caught=1") {
		t.Errorf("gate.log does not file the consumed measurement as the measurement it is:\n%s",
			gateLogText(t, cfgDir))
	}
}

// A consumed verdict is a verdict: an unaccepted survivor in it refuses the
// merge and is named, exactly as a local measurement's would be. Routing the
// measurement to a box that can make it is worth nothing if the answer it
// brings back cannot stop anything.
func TestMeasure_AnUnacceptedSurvivorInAConsumedRunnerVerdictRefusesTheMergeByName(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	tree := mustTreeID(t, root)
	t.Cleanup(SetMutantsGOOSForTest("windows"))
	t.Cleanup(setRunnerReportForTest(func(string, string) (RunnerReport, string) {
		return RunnerReport{Tree: tree, Runner: "self-hosted linux", Mutants: []MutantOutcome{
			{File: "calc.go", Line: 3, Col: 39, Mutation: "ARITHMETIC_BASE", Status: "missed"},
		}}, ""
	}))
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("verdict = %+v, want the merge refused on the survivor the runner found", v)
	}
	const survivor = "calc.go:3:39: ARITHMETIC_BASE"
	if !strings.Contains(v.Message, survivor) {
		t.Errorf("the refusal never names the survivor %q:\n%s", survivor, v.Message)
	}
	if len(v.Unaccepted) != 1 {
		t.Errorf("unaccepted = %+v, want the one survivor judged against this repo's accept-list", v.Unaccepted)
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-refused:tested=1") {
		t.Errorf("gate.log does not file the refusal:\n%s", gateLogText(t, cfgDir))
	}
}

// No verdict for this tree — the runner is down, the run has not finished, or
// it never ran at all — is the outcome #699 already built, reached by the same
// measureUnmeasured and not a second path beside it. It does not block: a box
// that cannot measure must still be able to merge, and a gate that refused
// every merge on runner availability would be traded away within a week.
func TestMeasure_NoRunnerVerdictForThisTreeIsNotMeasuredAndBlocksNothing(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(SetMutantsGOOSForTest("windows"))
	t.Cleanup(setRunnerReportForTest(func(string, string) (RunnerReport, string) {
		return RunnerReport{}, "the run measuring this tree has not finished"
	}))
	var log strings.Builder

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.NotMeasured == "" || v.Refused || v.Skipped != "" {
		t.Errorf("verdict = %+v, want NOT MEASURED, blocking nothing, and not filed as a routine skip", v)
	}
	for _, want := range []string{"NOT MEASURED", "has not finished"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("merge-time output never says %q:\n%s", want, log.String())
		}
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-unmeasured:") {
		t.Errorf("gate.log does not file the gap:\n%s", gateLogText(t, cfgDir))
	}
}

// The identity has to be the identity of the tree being JUDGED, which at the
// moment this gate runs is not the tree of any commit. GatePRMerge checks
// trunk out in a throwaway worktree and merges the lane in with --no-commit,
// and the pre-merge-commit hook fires in that same state: HEAD is still
// trunk, and the merge result exists only in the index and the working tree.
// An identity read off HEAD would therefore name the code that is NOT
// landing, and every measurement of the lane would be rejected as measuring a
// different tree — the failure with the quiet symptom, since "no evidence"
// looks the same whether the binding is too strict or the runner is down.
func TestMutantsTreeID_NamesTheMergedTreeRatherThanHEADs(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := makeStagedMergeRepo(t, laneSource)

	id, why := mutantsTreeID(root)

	if id == "" {
		t.Fatalf("no identity for a merged tree that exists only in the index: %s", why)
	}
	if head := strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD^{tree}")); id == head {
		t.Errorf("tree id = %s, which is HEAD's tree — that is trunk, not the merge being judged", id)
	}
	if lane := strings.TrimSpace(gitOutT(t, root, "rev-parse", "lane^{tree}")); id != lane {
		t.Errorf("tree id = %s, want the merged tree %s that the staged merge produces", id, lane)
	}
}

// mustTreeID is the tree the gate is judging, read the way the gate reads it.
func mustTreeID(t *testing.T, root string) string {
	t.Helper()
	id, why := mutantsTreeID(root)
	if id == "" {
		t.Fatalf("mutantsTreeID(%s): %s", root, why)
	}
	return id
}

// The artifact name is the lookup key a runner's measurement is published
// and found under; it has to be a deterministic join of the fixed prefix and
// the tree id, since that is the one request the fetch makes instead of a
// scan of every artifact ever published.
func TestRunnerArtifactName_JoinsThePrefixAndTheTree(t *testing.T) {
	got := runnerArtifactName("abc123")
	const want = "mutants-verdict-abc123"
	if got != want {
		t.Errorf("runnerArtifactName(%q) = %q, want %q", "abc123", got, want)
	}
}

// git write-tree can succeed and still name nothing — an empty index in an
// otherwise fine repo prints no id and exits 0. That is as unidentifiable as
// a git failure, and must refuse the same way: an empty id is never treated
// as a match for anything.
func TestMutantsTreeID_EmptyWriteTreeOutputIsNoIdentity(t *testing.T) {
	root := makeGoRepo(t)
	t.Cleanup(setGitDiffOutForTest(func(string, ...string) (string, string, error) {
		return "  \n", "", nil
	}))

	id, why := mutantsTreeID(root)

	if id != "" {
		t.Errorf("id = %q, want no identity from a write-tree that named nothing", id)
	}
	want := "git write-tree named no tree for " + root
	if why != want {
		t.Errorf("why = %q, want %q", why, want)
	}
}

// A report cannot be bound to a tree nobody can identify, so writeRunnerReport
// must refuse before it ever marshals anything — and leave no file behind for
// the next box to mistake for a real measurement.
func TestWriteRunnerReport_RefusesWhenTheTreeCannotBeIdentified(t *testing.T) {
	root := makeGoRepo(t)
	t.Cleanup(setGitDiffOutForTest(func(string, ...string) (string, string, error) {
		return "", "error: Entry 'calc.go' not uptodate. Cannot merge.", errors.New("exit status 128")
	}))
	out := filepath.Join(t.TempDir(), "report.json")
	var log strings.Builder

	writeRunnerReport(root, out, "main", []MutantOutcome{{File: "calc.go", Line: 1, Status: "caught"}}, &log)

	if _, err := os.Stat(out); err == nil {
		t.Errorf("wrote %s for a tree it could not identify", out)
	}
	if !strings.Contains(log.String(), "no report written") || !strings.Contains(log.String(), "not uptodate") {
		t.Errorf("log = %q, want it to say no report was written and why", log.String())
	}
}

// A report whose directory cannot be created is a report that cannot be
// published: the write is refused and logged rather than panicking or
// silently losing the measurement.
func TestWriteRunnerReport_RefusesWhenTheDirectoryCannotBeMade(t *testing.T) {
	root := makeGoRepo(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(blocker, "nested", "report.json")
	var log strings.Builder

	writeRunnerReport(root, out, "main", []MutantOutcome{{File: "calc.go", Line: 1, Status: "caught"}}, &log)

	if _, err := os.Stat(out); err == nil {
		t.Errorf("wrote %s under a path component that is a plain file", out)
	}
	if !strings.Contains(log.String(), "no report written") {
		t.Errorf("log = %q, want it to say no report was written", log.String())
	}
}

// The line a human reads at merge time names the runner by whatever CI set
// RUNNER_NAME to, when it set anything — that is GitHub's own name for the
// box, and more useful than a bare hostname on a fleet of identical runners.
func TestRunnerIdentity_PrefersRUNNER_NAMEWhenSet(t *testing.T) {
	t.Setenv("RUNNER_NAME", "self-hosted-linux-3")
	t.Cleanup(SetMutantsGOOSForTest("linux"))

	got := runnerIdentity()

	const want = "self-hosted-linux-3 (linux)"
	if got != want {
		t.Errorf("runnerIdentity() = %q, want %q", got, want)
	}
}

// Without RUNNER_NAME — a local run, or a runner that never set it — the
// identity falls back to the box's own hostname, so the line still says
// something a reader can place.
func TestRunnerIdentity_FallsBackToHostnameWithoutRUNNER_NAME(t *testing.T) {
	t.Setenv("RUNNER_NAME", "")
	t.Cleanup(SetMutantsGOOSForTest("linux"))
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Fatalf("os.Hostname(): %q, %v — this box cannot state the identity this test expects", host, err)
	}

	got := runnerIdentity()

	want := host + " (linux)"
	if got != want {
		t.Errorf("runnerIdentity() = %q, want %q", got, want)
	}
}
