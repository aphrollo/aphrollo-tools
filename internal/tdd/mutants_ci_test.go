package tdd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

// fakeGremlins replaces the tool with one that writes the given report (empty
// meaning it wrote nothing) and exits with the given code, recording the base
// it was scoped to.
func fakeGremlins(t *testing.T, report string, code int) *[]string {
	t.Helper()
	seen := &[]string{}
	prev := goMutantsRunFn
	goMutantsRunFn = func(_, baseSHA, outPath string, _ int) int {
		*seen = append(*seen, baseSHA)
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

// A mutation gate nobody runs is the failure mode this whole design is
// against, so the wiring is pinned rather than assumed: the pipeline carries
// the job, it invokes the runner in this package, and it keeps the receipt.
// The other half of the rule — that `mutants` is a REQUIRED check — lives in
// GitHub's branch protection and cannot be read from a test.
func TestPipeline_RunsTheMutationCheckOnEveryPullRequest(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	for want, why := range map[string]string{
		"\n  mutants:\n":         "the pipeline must declare a `mutants` job, which is the name branch protection requires",
		"gate mutants go --diff": "the job must run the diff-scoped CI runner, not the detached local job",
		"upload-artifact":        "the run's receipt is its evidence and must leave the runner",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("%s (looked for %q in .github/workflows/pipeline.yml)", why, want)
		}
	}
}

// The repo's own half of the deal: the proof is required before a merge, and
// it is NOT measured on the box that is trying to edit code.
func TestAphrolloToml_RequiresTheProofAndLeavesItToCI(t *testing.T) {
	root := repoRootForTest(t)
	if !aphrolloTomlFlag(root, "mutation-receipt") {
		t.Error("aphrollo.toml must keep `mutation-receipt = true`: the merge gate reads it")
	}
	if mutationRunsLocally(root) {
		t.Error("aphrollo.toml must say `mutants-local = false`: this repo's proof is measured on the CI runner")
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

	if len(*ran) != 1 || (*ran)[0] != "deadbeef" {
		t.Fatalf("gremlins was scoped to %v, want the given merge base", *ran)
	}
}
