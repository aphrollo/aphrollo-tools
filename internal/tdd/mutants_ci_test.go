package tdd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A gremlins report with one survivor and one kill, in the tool's own shape
// (testdata/gremlins_report.json is the captured original; this one varies
// the statuses the captured run does not carry).
const ciReport = `{"files":[
  {"file_name":"calc.go","mutations":[
    {"type":"CONDITIONALS_BOUNDARY","status":"LIVED","line":4,"column":5},
    {"type":"ARITHMETIC_BASE","status":"KILLED","line":9,"column":2}]}]}`

// allSkippedReport is what a --diff base that matches nothing produces: the
// analysis still lists every mutant it found and marks each one SKIPPED.
const allSkippedReport = `{"files":[
  {"file_name":"calc.go","mutations":[
    {"type":"CONDITIONALS_BOUNDARY","status":"SKIPPED","line":4,"column":5},
    {"type":"ARITHMETIC_BASE","status":"SKIPPED","line":9,"column":2}]}]}`

// timedOutReport carries one mutant nobody managed to measure.
const timedOutReport = `{"files":[
  {"file_name":"calc.go","mutations":[
    {"type":"CONDITIONALS_BOUNDARY","status":"TIMED OUT","line":4,"column":5},
    {"type":"ARITHMETIC_BASE","status":"KILLED","line":9,"column":2}]}]}`

// gremlinsCall is one invocation of the tool, as the runner asked for it.
type gremlinsCall struct {
	root, baseSHA string
	workers       int
	excludeFiles  []string
}

// fakeGremlins replaces the tool with one that writes the given report (empty
// meaning it wrote nothing) and exits with the given code, recording how it
// was called.
func fakeGremlins(t *testing.T, report string, code int) *[]gremlinsCall {
	t.Helper()
	seen := &[]gremlinsCall{}
	prev := goMutantsRunFn
	goMutantsRunFn = func(root, baseSHA, outPath string, workers int, excludeFiles []string) int {
		*seen = append(*seen, gremlinsCall{root: root, baseSHA: baseSHA, workers: workers, excludeFiles: excludeFiles})
		if report != "" {
			mustWrite(t, outPath, report)
		}
		return code
	}
	t.Cleanup(func() { goMutantsRunFn = prev })
	return seen
}

// ciRepo is a committed Go repo on a lane, the state a pull request is in.
func ciRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "calc.go", "package m\n\nfunc Calc() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")
	return root
}

// The CI runner is the CHECK, not a report. The local job writes a receipt
// somebody may read; this one runs on the pull request, and a survivor nobody
// accepted has to be the red that stops the merge.
func TestRunGoMutantsCI_FailsOnASurvivorNobodyAccepted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	fakeGremlins(t, ciReport, 0)

	var out bytes.Buffer
	code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123"}, &out)

	if code == 0 {
		t.Fatalf("an unaccepted survivor must fail the check, got exit 0:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "calc.go:4") {
		t.Fatalf("the output must name the survivor, got:\n%s", out.String())
	}
}

// The accept-list is the escape hatch, and it costs a stated reason. A run
// whose every survivor carries one is green.
func TestRunGoMutantsCI_PassesWhenEverySurvivorCarriesAReason(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		"mutation-accept = [",
		`  "calc.go:4 CONDITIONALS_BOUNDARY # the bound is the caller's own, pinned one layer up",`,
		"]",
	}, "\n"))
	fakeGremlins(t, ciReport, 0)

	var out bytes.Buffer
	if code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123"}, &out); code != 0 {
		t.Fatalf("an accepted survivor must not fail the check, got exit %d:\n%s", code, out.String())
	}
}

// The receipt is the run's evidence, and CI uploads it as an artifact — so it
// goes where the workflow says, signed the same way a local run signs it. An
// unsigned receipt is one anybody could have typed.
func TestRunGoMutantsCI_WritesASignedReceiptWhereTheWorkflowUploadsIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	fakeGremlins(t, ciReport, 0)
	path := filepath.Join(t.TempDir(), "receipt.json")

	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123", Receipt: path}, &bytes.Buffer{})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no receipt at the path the workflow uploads: %v", err)
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.MAC == "" {
		t.Fatal("the receipt is unsigned")
	}
	if res := verifyReceiptMAC(data, r.Repo, r.TipTree); res != nil {
		t.Fatalf("the receipt does not verify: %s", res.Message)
	}
	if r.MutantsTotal != 2 || r.Caught != 1 || len(r.Unaccepted) != 1 {
		t.Fatalf("receipt = %+v, want 2 mutants, 1 caught, 1 unaccepted survivor", r)
	}
	if r.BaseSHA != "abc123" {
		t.Fatalf("BaseSHA = %q, want the merge base the run was scoped to", r.BaseSHA)
	}
}

// A run that measured NO mutants is not a proof either. gremlins marks
// everything outside its --diff scope SKIPPED, and a scope that matches
// nothing is exactly what a stale or wrong merge base looks like — the same
// "walks nothing and passes" hole the path argument had, re-entering through
// the base. The merge gate accepts a zero-mutant RECEIPT (a diff with nothing
// mutable in it is a real answer); the CI judge refuses a zero-mutant RUN,
// because the run is the thing that could have been mis-scoped.
func TestRunGoMutantsCI_FailsWhenTheScopeMatchedNoMutants(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	fakeGremlins(t, allSkippedReport, 0)

	var out bytes.Buffer
	code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123"}, &out)

	if code == 0 {
		t.Fatalf("a run that measured no mutants must not pass, got exit 0:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "abc123") {
		t.Fatalf("the output must name the base whose scope matched nothing, got:\n%s", out.String())
	}
}

// ciRepoWith is ciRepo with a caller-chosen set of files on the lane, handing
// back the base the diff is taken against. Nothing depends on the order the
// files are written: they all land in one commit.
func ciRepoWith(t *testing.T, files map[string]string) (root, base string) {
	t.Helper()
	root = makeGoRepo(t)
	base = gitValue(t, root, "rev-parse", "HEAD")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	for rel, content := range files {
		write(t, root, rel, content)
	}
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")
	return root, base
}

// A pull request that changes no PRODUCTION Go measures zero mutants because
// there was nothing to mutate — YAML, markdown, a test file, a testdata
// fixture. That is a real answer about the lane, not a mis-scoped base, and
// the two have to be told apart before the zero is judged. Nothing is even
// spawned: gremlins mutates production Go only.
func TestRunGoMutantsCI_PassesWhenTheDiffCarriesNoMutableGo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := ciRepoWith(t, map[string]string{
		".github/workflows/pipeline.yml": "jobs: {}\n",
		"README.md":                      "# docs\n",
		"calc_test.go":                   "package m\n\nimport \"testing\"\n\nfunc TestCalc(t *testing.T) {}\n",
		"internal/x/testdata/fixture.go": "package fixture\n\nfunc Fixture() int { return 1 }\n",
	})
	ran := fakeGremlins(t, allSkippedReport, 0)
	path := filepath.Join(t.TempDir(), "receipt.json")

	var out bytes.Buffer
	if code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base, Receipt: path}, &out); code != 0 {
		t.Fatalf("a diff with no production Go must pass, got exit %d:\n%s", code, out.String())
	}
	if want := "0 mutable Go lines in " + base + "..HEAD: nothing to judge"; !strings.Contains(out.String(), want) {
		t.Fatalf("output = %q, want %q — the line that says why zero is a real answer", out.String(), want)
	}
	if len(*ran) != 0 {
		t.Fatalf("the tool was started for a diff with nothing to mutate: %+v", *ran)
	}
	// The receipt is still written: CI uploads it, and "measured zero, and
	// here is why" is a claim somebody may need to read.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no receipt for a run that legitimately measured nothing: %v", err)
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.MutantsTotal != 0 || r.MAC == "" {
		t.Fatalf("receipt = %+v, want a signed zero-mutant receipt", r)
	}
}

// The other side of the same rule, and the one that keeps it honest: one
// production .go file in the diff and nothing measured is a base that matched
// nothing, which is what a stale merge base looks like.
func TestRunGoMutantsCI_StillFailsWhenProductionGoChangedAndNothingWasMeasured(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := ciRepoWith(t, map[string]string{
		"README.md": "# docs\n",
		"calc.go":   "package m\n\nfunc Calc() int { return 1 }\n",
	})
	fakeGremlins(t, allSkippedReport, 0)

	var out bytes.Buffer
	code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base}, &out)

	if code == 0 {
		t.Fatalf("a production .go file changed and nothing was measured — that must not pass:\n%s", out.String())
	}
	if !strings.Contains(out.String(), base) {
		t.Fatalf("the output must name the base whose scope matched nothing, got:\n%s", out.String())
	}
}

// A timeout is an UNMEASURED mutant filed beside the measured ones. The merge
// gate refuses a receipt carrying one; with the local run off this check is
// the only judge the repo has, so it refuses the same fact in the same words.
func TestRunGoMutantsCI_FailsOnATimedOutMutant(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	fakeGremlins(t, timedOutReport, 0)

	var out bytes.Buffer
	code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123"}, &out)

	if code == 0 {
		t.Fatalf("a timed-out mutant must not pass, got exit 0:\n%s", out.String())
	}
	if want := mutantsTimedOutLine(1); !strings.Contains(out.String(), want) {
		t.Fatalf("output = %q, want the merge gate's own sentence %q", out.String(), want)
	}
}

// A tool that produced nothing measured nothing. Reading that as a pass is
// how a mutation gate becomes decoration.
func TestRunGoMutantsCI_FailsWhenTheToolWroteNoReport(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	fakeGremlins(t, "", 3)

	var out bytes.Buffer
	if code := RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123"}, &out); code == 0 {
		t.Fatalf("a run with no report must not pass, got exit 0:\n%s", out.String())
	}
}

// Without a base there is no diff to scope to, and an unscoped run measures
// the whole module — 1626 mutants in internal/tdd alone, each one a package
// re-run. It is refused rather than started.
func TestRunGoMutantsCI_RefusesToRunWithoutAMergeBase(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	ran := fakeGremlins(t, ciReport, 0)

	var out bytes.Buffer
	if code := RunGoMutantsCI(GoMutantsCI{Root: root}, &out); code == 0 {
		t.Fatalf("a run with no merge base must be refused, got exit 0:\n%s", out.String())
	}
	if len(*ran) != 0 {
		t.Fatalf("the tool was started anyway, scoped to %v", *ran)
	}
}

// fakeBoxJobs pins what the box would answer. Real machines answer 1 or 2, so
// a test that let the box speak for itself could not tell the caller's own 1
// from the box's.
func fakeBoxJobs(t *testing.T, n int) {
	t.Helper()
	prev := mutantsJobsFn
	mutantsJobsFn = func() (int, string) { return n, "pinned by the test" }
	t.Cleanup(func() { mutantsJobsFn = prev })
}

// gremlins re-runs the package's suite once per mutant, so an uncapped run
// owns the runner for as long as it takes. The cap is the caller's when it
// named one, and the box's own answer otherwise — never zero.
func TestRunGoMutantsCI_CapsTheRunAtTheWorkersItWasGiven(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	ran := fakeGremlins(t, ciReport, 0)
	fakeBoxJobs(t, 7)

	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123", Workers: 3}, &bytes.Buffer{})
	// 1 is the boundary the caller is most likely to name — a serial run on a
	// shared runner — and it is a number, not "ask the box".
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123", Workers: 1}, &bytes.Buffer{})
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "abc123"}, &bytes.Buffer{})

	if len(*ran) != 3 {
		t.Fatalf("the tool ran %d time(s), want 3", len(*ran))
	}
	if got := (*ran)[0].workers; got != 3 {
		t.Errorf("workers = %d, want the 3 the caller asked for", got)
	}
	if got := (*ran)[1].workers; got != 1 {
		t.Errorf("workers = %d, want the 1 the caller asked for", got)
	}
	if got := (*ran)[2].workers; got != 7 {
		t.Errorf("workers = %d with none named, want the box's own answer", got)
	}
}

// runCommandIn hands back the tool's own exit code. A spawn that failed read
// as 0 is a mutation gate that passes because the tool is missing.
func TestRunCommandIn_ReportsTheChildsExitCode(t *testing.T) {
	dir := t.TempDir()
	if got := runCommandIn(dir, gitBinary(), []string{"--version"}); got != 0 {
		t.Errorf("a command that succeeded reported %d, want 0", got)
	}
	if got := runCommandIn(dir, gitBinary(), []string{"cat-file", "-e", "nope"}); got == 0 {
		t.Error("a command that failed reported 0")
	}
	if got := runCommandIn(dir, "aphrollo-no-such-binary", nil); got == 0 {
		t.Error("a binary that is not installed reported 0")
	}
}

// CI hands the runner a directory, not a promise. From a SUBDIRECTORY the
// measurement still has to be the repo's: gremlins gathers coverage for the
// module it is started in, and started one level down it measures a fraction
// of what the pull request changed.
func TestRunGoMutantsCI_MeasuresFromTheRepoRootNotTheDirectoryItWasHanded(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	sub := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ran := fakeGremlins(t, ciReport, 0)

	RunGoMutantsCI(GoMutantsCI{Root: sub, BaseSHA: "abc123"}, &bytes.Buffer{})

	if len(*ran) != 1 {
		t.Fatalf("the tool ran %d time(s), want 1", len(*ran))
	}
	if got := (*ran)[0].root; got != root {
		t.Fatalf("gremlins ran in %q, want the repo root %q", got, root)
	}
}

// A mutation gate nobody runs is the failure mode this whole design is
// against, so the wiring is pinned rather than assumed: the pipeline carries
// the job, it invokes the runner in this package, and it keeps the receipt.
// The job is a tripwire, not a gate: a merge is judged locally under the
// pre-merge-commit gate, and this run on main records an escape when it
// disagrees. Nothing waits for it, so it never runs on a pull request.
func TestPipeline_RunsTheMutationCheckOnPushesToMainOnly(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	for want, why := range map[string]string{
		"\n  mutants:\n":                      "the pipeline must declare a `mutants` job: the tripwire that runs after a local merge lands",
		"    if: github.event_name == 'push'": "the job runs on pushes to main only; a pull request is never judged by it, the local pre-merge gate is",
		"gate mutants go --diff":              "the job must run the diff-scoped CI runner, not the detached local job",
		"upload-artifact":                     "the run's receipt is its evidence and must leave the runner",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("%s (looked for %q in .github/workflows/pipeline.yml)", why, want)
		}
	}
}

// The repo's own half of the deal, pinned through the functions that actually
// READ these keys rather than through the file: mutationReceiptOptIn is the
// merge gate's own reader (mutationReceiptStage calls it), and
// mutationRunsLocally is what the post-commit hook and that same stage consult
// to decide whether a local run exists to demand a receipt from.
func TestAphrolloToml_RequiresTheProofAndMeasuresItLocally(t *testing.T) {
	root := repoRootForTest(t)
	if !mutationReceiptOptIn(root) {
		t.Error("aphrollo.toml must keep `mutation-receipt = true`: mutationReceiptStage reads it through mutationReceiptOptIn")
	}
	if !mutationRunsLocally(root) {
		t.Error("aphrollo.toml must say `mutants-local = true`: the post-commit job on this box writes the receipt the pre-merge gate consumes; CI runs only the tripwire on main")
	}
}

// repoRootForTest is the checkout the tests are running inside. `go test`
// starts in the package directory, so there is always one.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	root := RepoRoot(".")
	if root == "" {
		t.Fatal("these tests read this repo's own tree and could not find its root")
	}
	return root
}

// repoFile reads one of THIS repo's own tracked files.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{repoRootForTest(t)}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The run is scoped to the base the workflow computed, not to a default the
// runner picked for itself.
func TestRunGoMutantsCI_ScopesTheRunToTheGivenMergeBase(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := ciRepo(t)
	ran := fakeGremlins(t, ciReport, 0)

	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: "deadbeef"}, &bytes.Buffer{})

	if len(*ran) != 1 || (*ran)[0].baseSHA != "deadbeef" {
		t.Fatalf("gremlins was scoped to %+v, want the given merge base", *ran)
	}
}

// twoFilesReport is what a --diff base finds over a lane touching two files,
// both killed.
const twoFilesReport = `{"files":[
  {"file_name":"calc.go","mutations":[
    {"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":2}]},
  {"file_name":"other.go","mutations":[
    {"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":2}]}]}`

// twoModulesReport is what a --diff base finds over a lane touching one file
// in each of two separate modules, both killed.
const twoModulesReport = `{"files":[
  {"file_name":"calc.go","mutations":[
    {"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":2}]},
  {"file_name":"pkg2/other.go","mutations":[
    {"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":2}]}]}`

// onlyPkg2Report is what a --diff base finds over a lane touching just
// pkg2/other.go — the shape a NARROWED, excluded-files run produces.
const onlyPkg2Report = `{"files":[
  {"file_name":"pkg2/other.go","mutations":[
    {"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":2}]}]}`

// A second push over a tree UNCHANGED since the last measured run is the
// case issue #143 exists for: every mutant the store already answers for is
// carried, and gremlins is not even started (0 measured).
func TestRunGoMutantsCI_SecondRunOverAnUnchangedTreeMeasuresZeroAndCarriesEverything(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := ciRepoWith(t, map[string]string{
		"calc.go":  "package m\n\nfunc Calc() int { return 1 }\n",
		"other.go": "package m\n\nfunc Other() int { return 2 }\n",
	})
	store := t.TempDir()

	ran1 := fakeGremlins(t, twoFilesReport, 0)
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base, Store: store}, &bytes.Buffer{})
	if len(*ran1) != 1 {
		t.Fatalf("the first push must measure — the store starts empty, got %d call(s)", len(*ran1))
	}

	ran2 := fakeGremlins(t, twoFilesReport, 0)
	var out bytes.Buffer
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base, Store: store}, &out)

	if len(*ran2) != 0 {
		t.Fatalf("a second run over an UNCHANGED tree started gremlins %d time(s), want 0: %+v", len(*ran2), *ran2)
	}
	if want := "0 measured, 2 carried"; !strings.Contains(out.String(), want) {
		t.Fatalf("output = %q, want it to report %q", out.String(), want)
	}
}

// A changed file re-runs that file's own package's mutants; a file in a
// DIFFERENT package the push never touched carries its prior verdict — the
// partial-carry shape issue #143 asks for, proven through the seam rather
// than a real gremlins (which cannot run in this sandbox). The fence is
// keyed by MODULE (nearest go.mod), so the two files need their own go.mod
// to fall in different fences at all: one shared module would invalidate
// both on either one's change, by design (mutants_plan.go).
func TestRunGoMutantsCI_AChangedFileReRunsItsMutantsCarryingTheUnchangedOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := ciRepoWith(t, map[string]string{
		"calc.go":       "package m\n\nfunc Calc() int { return 1 }\n",
		"pkg2/go.mod":   "module pkg2\n\ngo 1.24\n",
		"pkg2/other.go": "package pkg2\n\nfunc Other() int { return 2 }\n",
	})
	store := t.TempDir()

	fakeGremlins(t, twoModulesReport, 0)
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base, Store: store}, &bytes.Buffer{})

	// The second push touches pkg2/other.go only — calc.go's blob (and its
	// own module's fence) is unchanged.
	write(t, root, "pkg2/other.go", "package pkg2\n\nfunc Other() int { return 3 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "second push")

	ran2 := fakeGremlins(t, onlyPkg2Report, 0)
	var out bytes.Buffer
	RunGoMutantsCI(GoMutantsCI{Root: root, BaseSHA: base, Store: store}, &out)

	if len(*ran2) != 1 {
		t.Fatalf("the second push must start gremlins exactly once, got %d call(s)", len(*ran2))
	}
	if got := (*ran2)[0].excludeFiles; !slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "calc.go") }) {
		t.Fatalf("excludeFiles = %v, want calc.go excluded — its blob and its own module's fence never changed", got)
	}
	if want := "1 measured, 1 carried"; !strings.Contains(out.String(), want) {
		t.Fatalf("output = %q, want it to report %q", out.String(), want)
	}
}

// An empty --store must resolve to the machine-local default MutantStorePath
// already uses, never a bare "" path a later os.MkdirAll("", ...) silently
// no-ops on — that would write outcomes.json into whatever the CURRENT
// working directory happens to be, corrupting an unrelated tree.
func TestCiMutantStorePath_EmptyStoreFallsBackToTheMachineLocalDefault(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()

	got := ciMutantStorePath("", repo)
	want := MutantStorePath(repo)

	if got != want {
		t.Fatalf("ciMutantStorePath(%q, repo) = %q, want the machine-local default %q", "", got, want)
	}
}

// An explicit --store <dir> (CI's actions/cache path) must win over the
// machine-local default, and land the outcomes file directly under it.
func TestCiMutantStorePath_ExplicitStoreWinsOverTheMachineLocalDefault(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	store := filepath.Join(t.TempDir(), "cache-dir")

	got := ciMutantStorePath(store, repo)
	want := filepath.Join(store, "outcomes.json")

	if got != want {
		t.Fatalf("ciMutantStorePath(%q, repo) = %q, want %q", store, got, want)
	}
	if got == MutantStorePath(repo) {
		t.Fatalf("an explicit --store must never resolve to the same path as the machine-local default")
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatalf("ciMutantStorePath must create the store directory, got err=%v", err)
	}
}
