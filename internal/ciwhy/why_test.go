package ciwhy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// runViewJSON is the field list every run-view call asks gh for; the fakes
// key on the whole argv, so a test fails loudly if the call ever changes.
const runViewJSON = "attempt,conclusion,databaseId,event,headBranch,headSha,jobs,status,url,workflowName"

// fakeGh answers each gh argv from a recorded fixture and refuses any call it
// was not given, so a test proves both the output and that nothing else was
// asked of the network.
type fakeGh struct {
	t       *testing.T
	answers map[string]fakeAnswer
}

type fakeAnswer struct {
	file string // a testdata file whose bytes gh prints
	text string // inline output when there is no file
	err  error
}

func (f *fakeGh) run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	a, ok := f.answers[key]
	if !ok {
		f.t.Errorf("unexpected gh call: gh %s", key)
		return nil, errors.New("unexpected gh call")
	}
	if a.file == "" {
		return []byte(a.text), a.err
	}
	b, err := os.ReadFile("testdata/" + a.file)
	if err != nil {
		f.t.Fatalf("fixture %s: %v", a.file, err)
	}
	return b, a.err
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func runWhy(t *testing.T, f *fakeGh, target Target) string {
	t.Helper()
	var out bytes.Buffer
	if err := Why(testCtx(t), f.run, target, &out); err != nil {
		t.Fatalf("Why: %v", err)
	}
	return out.String()
}

func assertOutput(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestWhy_GoTestFailureListsTheFailingTestItsAssertionAndItsPackage(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 35939585713 --json " + runViewJSON:                   {file: "35939585713.run.json"},
		"api repos/{owner}/{repo}/check-runs/107444355625/annotations": {file: "annotations-107444355625.json"},
		"run view --job 107444355625 --log-failed":                     {file: "35939585713.job-107444355625.log"},
	}}
	got := runWhy(t, f, Target{Run: 35939585713, Workflow: "Pipeline"})
	want := "run 35939585713 Pipeline attempt 1: failure (push main 58de4e5)\n" +
		"https://github.com/aphrollo/aphrollo-tools/actions/runs/35939585713\n" +
		"test: failure at step 9 \"Test (race + shuffle)\"\n" +
		"  --- FAIL: TestCommitMsg_AnAmendOfAMergeKeepsThePremergeGreenForItsUnchangedTree (2.15s)\n" +
		"      gatehooks_amend_test.go:100: premise broken — the merge gate refused the merge: exit 1\n" +
		"          gate premerge: golangci-lint could not run in /tmp/TestCommitMsg_AnAmendOfAMergeKeepsThePremergeGreenForItsUnchange76002285/003/x — another golangci-lint instance (started outside this gate) still holds its own machine-wide lock, so nothing was linted. This is box contention, not a finding about the code. Retry once that lint finishes.\n" +
		"          command: golangci-lint run --allow-serial-runners ./pkgx\n" +
		"  FAIL\tgithub.com/aphrollo/aphrollo-tools/internal/cli\t42.111s\n"
	assertOutput(t, got, want)
}

func TestWhy_GoTestFailureWithoutVerboseTakesTheAssertionsUnderTheHeader(t *testing.T) {
	// Without -v, go test prints a failing test's lines AFTER its --- FAIL
	// header, subtests nested under their parent. Shape from `go test` 1.26.
	log := "test\tgo test\t2026-09-24T00:44:27.0000000Z --- FAIL: TestA (0.00s)\n" +
		"test\tgo test\t2026-09-24T00:44:27.0000001Z     --- FAIL: TestA/sub (0.00s)\n" +
		// A property that passed inside the failing test: noise, not evidence.
		"test\tgo test\t2026-09-24T00:44:27.0000002Z         a_test.go:5: [rapid] OK, passed 100 tests (1.2ms)\n" +
		"test\tgo test\t2026-09-24T00:44:27.0000002Z         a_test.go:7: want 2, got 3\n" +
		"test\tgo test\t2026-09-24T00:44:27.0000002Z             diff: -2 +3\n" +
		// Same indentation as the assertion but not written by it: not its
		// continuation, so it is not kept.
		"test\tgo test\t2026-09-24T00:44:27.0000002Z         stray output at the assertion's own depth\n" +
		"test\tgo test\t2026-09-24T00:44:27.0000003Z --- SKIP: TestB (0.00s)\n" +
		"test\tgo test\t2026-09-24T00:44:27.0000004Z     b_test.go:3: not on this host\n" +
		"test\tgo test\t2026-09-24T00:44:27.0000005Z FAIL\n" +
		"test\tgo test\t2026-09-24T00:44:27.0000006Z FAIL\texample.com/a\t0.01s\n"
	var out bytes.Buffer
	summariseLog(&out, parseLog([]byte(log)))
	want := "  --- FAIL: TestA (0.00s)\n" +
		"      --- FAIL: TestA/sub (0.00s)\n" +
		"          a_test.go:7: want 2, got 3\n" +
		"              diff: -2 +3\n" +
		"  FAIL\texample.com/a\t0.01s\n"
	assertOutput(t, out.String(), want)
}

func TestWhy_MutationJobListsSurvivorsOnceAndCollapsesInconclusive(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 35995898386 --json " + runViewJSON:                   {file: "35995898386.run.json"},
		"api repos/{owner}/{repo}/check-runs/107620766328/annotations": {file: "annotations-107620766328.json"},
		"run view --job 107620766328 --log-failed":                     {file: "35995898386.job-107620766328.log"},
	}}
	got := runWhy(t, f, Target{Run: 35995898386, Workflow: "Pipeline"})
	want := "run 35995898386 Pipeline attempt 1: failure (pull_request lane/probe-discard 6756081)\n" +
		"https://github.com/aphrollo/aphrollo-tools/actions/runs/35995898386\n" +
		"mutants-verdict: failure at step 9 \"aphrollo gate mutants run --report\"\n" +
		"  mutants: 80 tested, 65 caught, 0 unviable, 5 missed (0 accepted), 0 unmeasured, 9 not covered, 1 inconclusive\n" +
		"  internal/cli/probe_discard.go:195:55 CONDITIONALS_NEGATION (survived)\n" +
		"  internal/cli/probe_discard.go:257:77 CONDITIONALS_NEGATION (survived)\n" +
		"  internal/cli/probe_discard_backup.go:63:18 CONDITIONALS_BOUNDARY (survived)\n" +
		"  internal/cli/probe_discard_backup.go:88:53 CONDITIONALS_NEGATION (survived)\n" +
		"  internal/cli/probe_discard_backup.go:92:29 CONDITIONALS_NEGATION (survived)\n" +
		"  1 inconclusive mutant not listed (--raw prints it)\n"
	assertOutput(t, got, want)
}

func TestWhy_MutationJobReportsATimedOutMutantWithItsStatus(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 35952375676 --json " + runViewJSON:                   {file: "35952375676.run.json"},
		"api repos/{owner}/{repo}/check-runs/107485510485/annotations": {file: "annotations-107485510485.json"},
		"run view --job 107485510485 --log-failed":                     {file: "35952375676.job-107485510485.log"},
	}}
	got := runWhy(t, f, Target{Run: 35952375676, Workflow: "Pipeline"})
	want := "run 35952375676 Pipeline attempt 1: failure (pull_request lane/postedit-integration 09b6f0a)\n" +
		"https://github.com/aphrollo/aphrollo-tools/actions/runs/35952375676\n" +
		"mutants-verdict: failure at step 9 \"aphrollo gate mutants run --report\"\n" +
		"  mutants: 17 tested, 11 caught, 0 unviable, 0 missed (0 accepted), 1 unmeasured, 2 not covered, 3 inconclusive\n" +
		"  internal/tdd/suite/cargo_notrun.go:55:5 INCREMENT_DECREMENT (timed out twice, unmeasured)\n" +
		"  3 inconclusive mutants not listed (--raw prints them)\n"
	assertOutput(t, got, want)
}

func TestWhy_RunnerLostCommunicationIsInfraNotTestOutput(t *testing.T) {
	// Attempt 1 of run 35950366790, the one whose runner dropped: no log is
	// fetched, because nothing in it is the reason.
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 35950366790 --json " + runViewJSON:                   {file: "35950366790.run.json"},
		"api repos/{owner}/{repo}/check-runs/107477452842/annotations": {file: "annotations-107477452842.json"},
	}}
	got := runWhy(t, f, Target{Run: 35950366790, Workflow: "Pipeline"})
	want := "run 35950366790 Pipeline attempt 1: failure (pull_request lane/postedit-integration d2eff3c)\n" +
		"https://github.com/aphrollo/aphrollo-tools/actions/runs/35950366790\n" +
		"mutants-verdict: infra: runner lost communication\n" +
		"  The self-hosted runner lost communication with the server. Verify the machine is running and has a healthy network connection. Anything in your workflow that terminates the runner process, starves it for CPU/Memory, or blocks its network access can cause this error.\n"
	assertOutput(t, got, want)
}

func TestWhy_CancelledJobsAreInfraAndFetchNothing(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 34234123030 --json " + runViewJSON: {file: "34234123030.run.json"},
	}}
	got := runWhy(t, f, Target{Run: 34234123030, Workflow: "Pipeline"})
	want := "run 34234123030 Pipeline attempt 1: cancelled (pull_request lane/mutants-parallel 65be21e)\n" +
		"https://github.com/aphrollo/aphrollo-tools/actions/runs/34234123030\n" +
		"test: infra: cancelled\n" +
		"deploy: infra: cancelled\n"
	assertOutput(t, got, want)
}

// oneJobRun is a run-view answer with one failed job, for the branches no
// captured run in this repo exhibits (job-level timeout, an expired log).
func oneJobRun(conclusion string) string {
	return `{"attempt":1,"conclusion":"failure","databaseId":7000000001,"event":"push",` +
		`"headBranch":"main","headSha":"abcdef0123456789","status":"completed",` +
		`"url":"https://example.invalid/runs/7000000001","workflowName":"Pipeline",` +
		`"jobs":[{"databaseId":9001,"name":"build","conclusion":"` + conclusion + `","status":"completed",` +
		`"steps":[{"name":"Build","number":3,"conclusion":"` + conclusion + `","status":"completed"}]}]}`
}

func TestWhy_JobLevelTimeoutIsInfra(t *testing.T) {
	// GitHub's own wording for a job that hit timeout-minutes.
	ann := `[{"annotation_level":"failure","message":"The job running on runner aphrollo-2 has exceeded the maximum execution time of 30 minutes."}]`
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 7000000001 --json " + runViewJSON:            {text: oneJobRun("failure")},
		"api repos/{owner}/{repo}/check-runs/9001/annotations": {text: ann},
	}}
	got := runWhy(t, f, Target{Run: 7000000001, Workflow: "Pipeline"})
	want := "run 7000000001 Pipeline attempt 1: failure (push main abcdef0)\n" +
		"https://example.invalid/runs/7000000001\n" +
		"build: infra: timed out at job level\n" +
		"  The job running on runner aphrollo-2 has exceeded the maximum execution time of 30 minutes.\n"
	assertOutput(t, got, want)
}

func TestWhy_TimedOutConclusionIsInfraWithoutAnAnnotation(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 7000000001 --json " + runViewJSON: {text: oneJobRun("timed_out")},
	}}
	got := runWhy(t, f, Target{Run: 7000000001, Workflow: "Pipeline"})
	if !strings.Contains(got, "build: infra: timed out at job level\n") {
		t.Errorf("a timed_out job must read as a job-level timeout, got:\n%s", got)
	}
}

func TestWhy_MissingJobLogIsInfraNotAnError(t *testing.T) {
	// The storage error behind an expired log, and gh's own wording for it.
	for _, ghSays := range []string{
		"failed to get run log: HTTP 404: <Error><Code>BlobNotFound</Code><Message>The specified blob does not exist.</Message></Error>\n",
		"failed to get run log: log not found: 9001\n",
	} {
		f := &fakeGh{t: t, answers: map[string]fakeAnswer{
			"run view 7000000001 --json " + runViewJSON:            {text: oneJobRun("failure")},
			"api repos/{owner}/{repo}/check-runs/9001/annotations": {text: "[]"},
			"run view --job 9001 --log-failed":                     {text: ghSays, err: errors.New("exit status 1")},
		}}
		got := runWhy(t, f, Target{Run: 7000000001, Workflow: "Pipeline"})
		if !strings.Contains(got, "build: infra: job log not found (BlobNotFound)\n") {
			t.Errorf("gh saying %q must read as infra, got:\n%s", ghSays, got)
		}
	}
}

func TestWhy_AnyOtherLogFetchFailureIsAnError(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 7000000001 --json " + runViewJSON:            {text: oneJobRun("failure")},
		"api repos/{owner}/{repo}/check-runs/9001/annotations": {text: "[]"},
		"run view --job 9001 --log-failed":                     {text: "HTTP 403: rate limit exceeded\n", err: errors.New("exit status 1")},
	}}
	err := Why(testCtx(t), f.run, Target{Run: 7000000001, Workflow: "Pipeline"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("want gh's own error text, got %v", err)
	}
}

func TestWhy_OtherFailureShowsTheLastFifteenNonNoiseLines(t *testing.T) {
	var log strings.Builder
	log.WriteString("build\tBuild\t\xef\xbb\xbf2026-09-24T00:00:00.0000000Z ##[group]Run make\n")
	log.WriteString("build\tBuild\t2026-09-24T00:00:00.0000000Z ##[endgroup]\n")
	for i := 1; i <= 20; i++ {
		log.WriteString("build\tBuild\t2026-09-24T00:00:01.0000000Z line " + string(rune('a'+i-1)) + "\n")
		log.WriteString("build\tBuild\t2026-09-24T00:00:01.0000000Z \n")
	}
	log.WriteString("build\tBuild\t2026-09-24T00:00:02.0000000Z ##[error]Process completed with exit code 2.\n")
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 7000000001 --json " + runViewJSON:            {text: oneJobRun("failure")},
		"api repos/{owner}/{repo}/check-runs/9001/annotations": {text: "[]"},
		"run view --job 9001 --log-failed":                     {text: log.String()},
	}}
	got := runWhy(t, f, Target{Run: 7000000001, Workflow: "Pipeline"})
	want := "run 7000000001 Pipeline attempt 1: failure (push main abcdef0)\n" +
		"https://example.invalid/runs/7000000001\n" +
		"build: failure at step 3 \"Build\"\n" +
		"  line g\n  line h\n  line i\n  line j\n  line k\n  line l\n  line m\n" +
		"  line n\n  line o\n  line p\n  line q\n  line r\n  line s\n  line t\n" +
		"  ##[error]Process completed with exit code 2.\n"
	assertOutput(t, got, want)
}

func TestWhy_PRResolvesToTheLatestPipelineRunOnItsHeadCommit(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"pr view 838 --json headRefOid": {text: `{"headRefOid":"34234123030sha"}`},
		"run list --commit 34234123030sha --workflow Pipeline --limit 1 --json databaseId": {text: `[{"databaseId":34234123030}]`},
		"run view 34234123030 --json " + runViewJSON:                                       {file: "34234123030.run.json"},
	}}
	got := runWhy(t, f, Target{PR: 838, Workflow: "Pipeline"})
	if !strings.HasPrefix(got, "run 34234123030 ") {
		t.Errorf("PR 838 must resolve to run 34234123030, got:\n%s", got)
	}
}

func TestWhy_NoArgumentResolvesTheCurrentBranchPR(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"pr view --json headRefOid": {text: `{"headRefOid":"feed"}`},
		"run list --commit feed --workflow Pipeline --limit 1 --json databaseId": {text: `[{"databaseId":34234123030}]`},
		"run view 34234123030 --json " + runViewJSON:                             {file: "34234123030.run.json"},
	}}
	got := runWhy(t, f, Target{Workflow: "Pipeline"})
	if !strings.HasPrefix(got, "run 34234123030 ") {
		t.Errorf("the current branch's PR must resolve to run 34234123030, got:\n%s", got)
	}
}

func TestWhy_MainResolvesTheLatestMainRun(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run list --branch main --workflow Pipeline --limit 1 --json databaseId": {text: `[{"databaseId":35939585713}]`},
		"run view 35939585713 --json " + runViewJSON:                             {file: "35939585713.run.json"},
		"api repos/{owner}/{repo}/check-runs/107444355625/annotations":           {file: "annotations-107444355625.json"},
		"run view --job 107444355625 --log-failed":                               {file: "35939585713.job-107444355625.log"},
	}}
	got := runWhy(t, f, Target{Main: true, Workflow: "Pipeline"})
	if !strings.HasPrefix(got, "run 35939585713 ") {
		t.Errorf("--main must resolve to run 35939585713, got:\n%s", got)
	}
}

func TestWhy_ACommitWithNoPipelineRunFailsLoud(t *testing.T) {
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"pr view 5 --json headRefOid":                                            {text: `{"headRefOid":"feed"}`},
		"run list --commit feed --workflow Pipeline --limit 1 --json databaseId": {text: `[]`},
	}}
	err := Why(testCtx(t), f.run, Target{PR: 5, Workflow: "Pipeline"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no Pipeline run") {
		t.Errorf("want a no-run error naming the workflow, got %v", err)
	}
}

func TestWhy_ASucceededRunSaysNothingFailed(t *testing.T) {
	run := `{"attempt":2,"conclusion":"success","databaseId":7000000002,"event":"push","headBranch":"main",` +
		`"headSha":"0123456789","status":"completed","url":"https://example.invalid/runs/7000000002",` +
		`"workflowName":"Pipeline","jobs":[{"databaseId":1,"name":"test","conclusion":"success","status":"completed","steps":[]},` +
		`{"databaseId":2,"name":"deploy","conclusion":"skipped","status":"completed","steps":[]}]}`
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 7000000002 --json " + runViewJSON: {text: run},
	}}
	got := runWhy(t, f, Target{Run: 7000000002, Workflow: "Pipeline"})
	want := "run 7000000002 Pipeline attempt 2: success (push main 0123456)\n" +
		"https://example.invalid/runs/7000000002\n" +
		"no failed job\n"
	assertOutput(t, got, want)
}

func TestRaw_PrintsTheFailedLogUntouched(t *testing.T) {
	b, err := os.ReadFile("testdata/35939585713.job-107444355625.log")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGh{t: t, answers: map[string]fakeAnswer{
		"run view 35939585713 --log-failed": {text: string(b)},
	}}
	var out bytes.Buffer
	if err := Raw(testCtx(t), f.run, Target{Run: 35939585713, Workflow: "Pipeline"}, &out); err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if !bytes.Equal(out.Bytes(), b) {
		t.Errorf("--raw must print gh's bytes unchanged: got %d bytes, want %d", out.Len(), len(b))
	}
}
