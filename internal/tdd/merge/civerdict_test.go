package merge

import (
	"io"
	"strings"
	"testing"
	"time"
)

// The pre-merge gate re-ran the whole suite on the merged tree even when CI
// had just judged that very tree. These tests pin when CI's verdict stands in
// for the local run and, as much, when it must not: a tree CI never saw, a
// check that is red or missing, a repo that turned reuse off, a repo whose
// mutation measurement is local.

// ciReuseLane is a lane whose merge into a moved trunk is clean, in a repo
// whose aphrollo.toml is the given [aphrollo] body, so the gate has a merged
// tree to judge.
func ciReuseLane(t *testing.T, body string) (root, trunk string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk = prGateLane(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\n"+body)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "declare the gate")
	return root, trunk
}

const mutantsInCI = "mutants-at-merge = \"ci\"\n"

func mergedTreeOf(t *testing.T, root, trunk string) string {
	t.Helper()
	out := gitOutT(t, root, "merge-tree", "--write-tree", trunk, "HEAD")
	return strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
}

var ciBefore = time.Now().Add(-time.Hour)

func passedCheck(name string) CIVerdictCheck {
	return CIVerdictCheck{Name: name, Passed: true, Started: ciBefore.Add(time.Minute),
		App: "github-actions", Workflow: "pipeline.yml", Attempt: 1}
}

// stubCIMergeRef makes CI's merge ref carry tree, made at committed, and
// counts the reads.
func stubCIMergeRef(t *testing.T, tree string, committed time.Time) *int {
	t.Helper()
	reads := 0
	orig := ciMergeRef
	ciMergeRef = func(string, int) (CIMergeRef, error) {
		reads++
		return CIMergeRef{Tree: tree, Committed: committed}, nil
	}
	t.Cleanup(func() { ciMergeRef = orig })
	return &reads
}

func ciVerdictOf(t *testing.T, root string, checks ...CIVerdictCheck) CIVerdict {
	t.Helper()
	return CIVerdict{PR: 7, HeadSHA: strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD")), Checks: checks}
}

func TestGatePRMergeReusingCI_ReusesTheVerdictOfTheSameTreeOnEveryOS(t *testing.T) {
	root, trunk := ciReuseLane(t, mutantsInCI)
	tree := mergedTreeOf(t, root, trunk)
	stubCIMergeRef(t, tree, ciBefore)

	var seen []gateRun
	var log strings.Builder
	v := ciVerdictOf(t, root, passedCheck("test"), passedCheck("test-windows (cli)"), passedCheck("test-windows (rest)"))
	if err := GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), &log, v); err != nil {
		t.Fatalf("a merged tree CI judged green on both OSes must land: %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("ran %d local suite(s) on a tree CI had judged: %+v", len(seen), seen)
	}
	want := "reused CI verdict for tree " + tree + " (linux, windows)"
	if !strings.Contains(log.String(), want) {
		t.Errorf("log lacks %q:\n%s", want, log.String())
	}
}

func TestGatePRMergeReusingCI_NamesOnlyTheOSesCIRan(t *testing.T) {
	root, trunk := ciReuseLane(t, mutantsInCI)
	tree := mergedTreeOf(t, root, trunk)
	stubCIMergeRef(t, tree, ciBefore)

	var log strings.Builder
	v := ciVerdictOf(t, root, passedCheck("test"))
	if err := GatePRMergeReusingCI(root, recordRuns(new([]gateRun), SuiteResult{Passed: true}), &log, v); err != nil {
		t.Fatal(err)
	}
	if want := "reused CI verdict for tree " + tree + " (linux)"; !strings.Contains(log.String(), want) {
		t.Errorf("log lacks %q:\n%s", want, log.String())
	}
}

// CI's per-PR jobs judge the PR's diff, never the merge, so the merged tree's
// own checks stay owed when the suites are taken from CI.
func TestGatePRMergeReusingCI_StillJudgesTheMergedTreesDocs(t *testing.T) {
	root, trunk := ciReuseLane(t, mutantsInCI)
	write(t, root, "go.mod", "module notes\n") // the doc check is on for a Go repo
	write(t, root, "NOTES.md", "See `crates/a/src/gone_missing.rs` for the details.\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "a doc citing a file that is not there")
	stubCIMergeRef(t, mergedTreeOf(t, root, trunk), ciBefore)

	var seen []gateRun
	var log strings.Builder
	err := GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), &log, ciVerdictOf(t, root, passedCheck("test")))
	if err == nil || !strings.Contains(err.Error(), "gone_missing.rs") {
		t.Fatalf("a dangling doc reference in the merged tree must refuse even on a reused verdict, got %v: %s", err, log.String())
	}
	if len(seen) != 0 {
		t.Fatalf("the suites ran %d time(s) on a reused verdict", len(seen))
	}
}

// ratchet: test_removed TestGatePRMergeReusingCI_RunsTheLocalSuiteWhenCIJudgedADifferentTree: a green verdict for an older base no longer runs the local suites; it refuses as stale (TestGatePRMergeReusingCI_DecidesByWhatCIJudged)
// ratchet: test_removed TestGatePRMergeReusingCI_RunsTheLocalSuiteWhenTheMergeRefIsNewerThanTheChecks: the same case, now a stale refusal (TestGatePRMergeReusingCI_DecidesByWhatCIJudged)

// What the gate does with a green CI verdict, by how it relates to the merge
// the gate would test: the same tree stands in for the suites, an older base is
// a stale verdict (rebase and let CI judge the new merge, never the 10 minute
// local run), and a verdict that never stood at all keeps the local path.
func TestGatePRMergeReusingCI_DecidesByWhatCIJudged(t *testing.T) {
	const (
		reuse = "reuse"
		stale = "stale"
		local = "local"
	)
	otherTree := "0000000000000000000000000000000000000001"
	cases := []struct {
		name      string
		upToDate  bool // the lane already holds trunk
		refTree   string
		refTime   time.Time
		checks    []CIVerdictCheck
		wantSuite bool
		want      string
	}{
		{name: "verdict of the same merge tree", checks: []CIVerdictCheck{passedCheck("test")}, want: reuse},
		{name: "lane already holds trunk", upToDate: true, checks: []CIVerdictCheck{passedCheck("test")}, want: reuse},
		{name: "trunk moved past what CI tested", refTree: otherTree, refTime: ciBefore, checks: []CIVerdictCheck{passedCheck("test"), passedCheck("test-windows (cli)")}, want: stale},
		{name: "merge ref rebuilt after the checks began", refTime: ciBefore.Add(time.Hour), checks: []CIVerdictCheck{passedCheck("test")}, want: stale},
		{name: "no linux check on this head", refTime: ciBefore, checks: []CIVerdictCheck{passedCheck("test-windows (cli)")}, want: local},
		{name: "a windows shard is red", refTime: ciBefore, checks: []CIVerdictCheck{passedCheck("test"), redCheck("test-windows (cli)")}, want: local},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, trunk := ciReuseLane(t, mutantsInCI)
			if tc.upToDate {
				gitDo(t, root, "merge", "-q", "--no-edit", trunk)
			}
			tree := tc.refTree
			if tree == "" {
				tree = mergedTreeOf(t, root, trunk)
			}
			if tc.upToDate {
				tree = "unused"
			}
			refTime := tc.refTime
			if refTime.IsZero() {
				refTime = ciBefore
			}
			stubCIMergeRef(t, tree, refTime)

			var seen []gateRun
			var log strings.Builder
			err := GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), &log, ciVerdictOf(t, root, tc.checks...))

			staleErr, isStale := AsStaleCIVerdict(err)
			switch tc.want {
			case reuse:
				if err != nil || len(seen) != 0 || !strings.Contains(log.String(), "reused CI verdict") {
					t.Fatalf("want a reuse with no suite; err=%v, suites=%d\n%s", err, len(seen), log.String())
				}
			case stale:
				if !isStale {
					t.Fatalf("want a stale-verdict refusal, got %v", err)
				}
				if len(seen) != 0 {
					t.Fatalf("a stale verdict ran %d local suite(s); it must refuse instead", len(seen))
				}
				base := strings.TrimSpace(gitOutT(t, root, "merge-base", "HEAD", trunk))
				now := strings.TrimSpace(gitOutT(t, root, "rev-parse", trunk))
				if staleErr.Base != base || staleErr.Trunk != now {
					t.Errorf("stale error names base %q and trunk %q, want %q and %q", staleErr.Base, staleErr.Trunk, base, now)
				}
				msg := err.Error()
				for _, want := range []string{"CI verdict is for base " + base[:7], trunk + " is now " + now[:7],
					"git rebase origin/" + trunk + " && git push --force-with-lease"} {
					if !strings.Contains(msg, want) {
						t.Errorf("refusal %q lacks %q", msg, want)
					}
				}
				if strings.Contains(msg, "\n") {
					t.Errorf("refusal must be one line, got %q", msg)
				}
			case local:
				if isStale || len(seen) == 0 {
					t.Fatalf("want the local suites (a verdict that never stood), got stale=%v suites=%d", isStale, len(seen))
				}
			}
		})
	}
}

func TestGatePRMergeReusingCI_RunsTheLocalSuiteWhenAnOSCheckIsNotGreen(t *testing.T) {
	cases := map[string][]CIVerdictCheck{
		"windows shard red":  {passedCheck("test"), passedCheck("test-windows (cli)"), redCheck("test-windows (rest)")},
		"linux test missing": {passedCheck("test-windows (cli)")},
		"linux test red":     {redCheck("test"), passedCheck("test-windows (cli)")},
	}
	for name, checks := range cases {
		t.Run(name, func(t *testing.T) {
			root, trunk := ciReuseLane(t, mutantsInCI)
			stubCIMergeRef(t, mergedTreeOf(t, root, trunk), ciBefore)
			var seen []gateRun
			_ = GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard, ciVerdictOf(t, root, checks...))
			if len(seen) == 0 {
				t.Fatal("a missing or red OS verdict was taken as a pass: no local suite ran")
			}
		})
	}
}

func TestGatePRMergeReusingCI_ReusesWithoutAMergeRefWhenTheLaneIsUpToDate(t *testing.T) {
	root, trunk := ciReuseLane(t, mutantsInCI)
	gitDo(t, root, "merge", "-q", "--no-edit", trunk)
	reads := stubCIMergeRef(t, "unused", ciBefore)

	var seen []gateRun
	var log strings.Builder
	if err := GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), &log, ciVerdictOf(t, root, passedCheck("test"))); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 || *reads != 0 {
		t.Fatalf("a lane already holding trunk needs neither a suite (%d ran) nor a merge ref (%d read)", len(seen), *reads)
	}
	if !strings.Contains(log.String(), "reused CI verdict for tree") {
		t.Errorf("no reuse line:\n%s", log.String())
	}
}

func TestGatePRMergeReusingCI_RunsTheLocalSuiteWhenCIVerdictsAreForAnotherHead(t *testing.T) {
	root, trunk := ciReuseLane(t, mutantsInCI)
	stubCIMergeRef(t, mergedTreeOf(t, root, trunk), ciBefore)
	v := ciVerdictOf(t, root, passedCheck("test"))
	v.HeadSHA = "deadbeef"

	var seen []gateRun
	_ = GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard, v)
	if len(seen) == 0 {
		t.Fatal("reused a verdict that belongs to another commit than the one being merged")
	}
}

func TestGatePRMergeReusingCI_ARepoCanTurnReuseOff(t *testing.T) {
	root, trunk := ciReuseLane(t, mutantsInCI+"ci-reuse = false\n")
	stubCIMergeRef(t, mergedTreeOf(t, root, trunk), ciBefore)

	var seen []gateRun
	err := GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard, ciVerdictOf(t, root, passedCheck("test")))
	if len(seen) == 0 {
		t.Fatalf("ci-reuse = false still skipped the local suite (gate said %v)", err)
	}
}

func TestGatePRMergeReusingCI_ALocalMutationMeasurementIsNeverSkipped(t *testing.T) {
	root, trunk := ciReuseLane(t, "mutants-at-merge = true\n")
	stubCIMergeRef(t, mergedTreeOf(t, root, trunk), ciBefore)

	var seen []gateRun
	// The measurement itself needs a toolchain and disk this test does not have; what
	// matters is that the gate went down the full path rather than reusing.
	_ = GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard, ciVerdictOf(t, root, passedCheck("test")))
	if len(seen) == 0 {
		t.Fatal("a repo measuring mutants at the merge had its gate skipped on CI's word")
	}
}

func TestReadCIReuse_RefusesAValueItCannotRead(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nci-reuse = maybe\n")
	if _, err := ReadCIReuse(root); err == nil || !strings.Contains(err.Error(), "ci-reuse") {
		t.Fatalf("want a refusal naming ci-reuse, got %v", err)
	}
}

func TestReadCIReuse_IsOnWhenUndeclared(t *testing.T) {
	on, err := ReadCIReuse(t.TempDir())
	if err != nil || !on {
		t.Fatalf("undeclared ci-reuse = (%v, %v), want on", on, err)
	}
}

// A re-run job keeps the merge commit it first tested but starts later, so a
// start time cannot say which merge it saw. Only a first attempt can be
// ordered against the merge ref; anything else, or an attempt GitHub did not
// say, is a verdict about a tree this gate cannot name.
func TestGatePRMergeReusingCI_RunsTheLocalSuiteForARerunOrAnUnknownAttempt(t *testing.T) {
	for name, attempt := range map[string]int{"rerun": 2, "unknown": 0} {
		t.Run(name, func(t *testing.T) {
			root, trunk := ciReuseLane(t, mutantsInCI)
			stubCIMergeRef(t, mergedTreeOf(t, root, trunk), ciBefore)
			c := passedCheck("test")
			c.Attempt = attempt
			var seen []gateRun
			_ = GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard, ciVerdictOf(t, root, c))
			if len(seen) == 0 {
				t.Fatalf("attempt %d was taken as the tree CI tested", attempt)
			}
		})
	}
}

// Any app can publish a check named `test`; only the pipeline's own job counts.
func TestGatePRMergeReusingCI_OnlyCountsTheChecksOfTheBoundWorkflowAndApp(t *testing.T) {
	cases := map[string]func(*CIVerdictCheck){
		"another app":      func(c *CIVerdictCheck) { c.App = "some-ci" },
		"another workflow": func(c *CIVerdictCheck) { c.Workflow = "other.yml" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root, trunk := ciReuseLane(t, mutantsInCI)
			stubCIMergeRef(t, mergedTreeOf(t, root, trunk), ciBefore)
			c := passedCheck("test")
			mutate(&c)
			var seen []gateRun
			_ = GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard, ciVerdictOf(t, root, c))
			if len(seen) == 0 {
				t.Fatal("a check from outside the bound workflow stood in for the suites")
			}
		})
	}
}

// A repo whose jobs are named otherwise says which ones carry its suites.
func TestGatePRMergeReusingCI_ARepoNamesTheChecksThatCarryItsSuites(t *testing.T) {
	root, trunk := ciReuseLane(t, mutantsInCI+"ci-reuse-checks = [\"unit\", \"unit-windows\"]\n"+"ci-reuse-workflow = \"ci.yml\"\n")
	tree := mergedTreeOf(t, root, trunk)
	stubCIMergeRef(t, tree, ciBefore)
	bound := func(name string) CIVerdictCheck {
		c := passedCheck(name)
		c.Workflow = "ci.yml"
		return c
	}
	var seen []gateRun
	var log strings.Builder
	err := GatePRMergeReusingCI(root, recordRuns(&seen, SuiteResult{Passed: true}), &log, ciVerdictOf(t, root, bound("unit"), bound("unit-windows (a)")))
	if err != nil || len(seen) != 0 {
		t.Fatalf("named checks green on the named workflow must be reused: err=%v, suites run=%d\n%s", err, len(seen), log.String())
	}
	if want := "reused CI verdict for tree " + tree + " (unit, unit-windows)"; !strings.Contains(log.String(), want) {
		t.Errorf("log lacks %q:\n%s", want, log.String())
	}
}

// The `test` job of a pull request that changes no code concludes success
// with its steps skipped: reuse is right, and the log must not claim tests ran.
func TestGatePRMergeReusingCI_SaysWhenCIRanNoTestsForANonCodeDiff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := prGateLane(t)
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "aphrollo.toml", "[aphrollo]\n"+mutantsInCI)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "declare the gate")
	gitDo(t, root, "checkout", "-q", "-b", "doclane")
	write(t, root, "NOTES.md", "Some notes.\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "notes")

	var log strings.Builder
	if err := GatePRMergeReusingCI(root, recordRuns(new([]gateRun), SuiteResult{Passed: true}), &log, ciVerdictOf(t, root, passedCheck("test"))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "reused CI verdict") || !strings.Contains(log.String(), "CI ran no tests: non-code diff") {
		t.Errorf("log lacks the reuse line with the non-code note:\n%s", log.String())
	}
}

func redCheck(name string) CIVerdictCheck {
	c := passedCheck(name)
	c.Passed = false
	return c
}
