package failfirst

import (
	"strings"
	"testing"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func pinRun(Runner, string) SuiteResult {
	return SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: passedAtHeadJSON}
}

func stagedBlob(t *testing.T, root, file string) string {
	t.Helper()
	out, err := git(root, "rev-parse", ":"+file)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// A test already green on the old code is accepted when a mutation proof
// recorded for that test on the staged content of the code under it exists.
// Serial: captures the process-wide os.Stderr.
func TestFailFirstStage_ARecordedMutationProofOfTheTestIsAcceptedForAPinTest(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := greenAtHeadRepo(t)
	core.RecordPinProof(root, core.PinProof{Test: "TestWidget", File: "widget.go", Blob: stagedBlob(t, root, "widget.go")})

	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, pinRun)
	})

	if res.Blocked {
		t.Fatalf("a pin test with a mutation proof must be accepted:\n%s", res.Message)
	}
}

// A proof of the code as it was before the file changed does not count.
// Serial: captures the process-wide os.Stderr.
func TestFailFirstStage_AMutationProofOfOtherContentIsStale(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := greenAtHeadRepo(t)
	core.RecordPinProof(root, core.PinProof{Test: "TestWidget", File: "widget.go", Blob: "0000000000000000000000000000000000000000"})

	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, pinRun)
	})

	if !res.Blocked {
		t.Fatal("a proof keyed to other content must not count")
	}
}

// A proof of another test says nothing about this one.
// Serial: captures the process-wide os.Stderr.
func TestFailFirstStage_AMutationProofOfAnotherTestDoesNotCount(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := greenAtHeadRepo(t)
	core.RecordPinProof(root, core.PinProof{Test: "TestGadget", File: "widget.go", Blob: stagedBlob(t, root, "widget.go")})

	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, pinRun)
	})

	if !res.Blocked {
		t.Fatal("a proof for a different test must not count")
	}
}

// The refusal names the proof route beside split-commit.
// Serial: captures the process-wide os.Stderr.
func TestFailFirstStage_RefusalNamesTheMutationProofRoute(t *testing.T) {
	tddtest.VerdictWordTmp(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := greenAtHeadRepo(t)

	var res GateResult
	captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, pinRun)
	})

	for _, want := range []string{
		"aphrollo gate split-commit -m",
		"aphrollo gate mutants prove --file <code file> --old <expr> --new <expr> --want-fail TestWidget",
	} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal lacks %q:\n%s", want, res.Message)
		}
	}
}

// Tests bundled with a manifest or lockfile are refused with that said
// directly, and the exact commands that split them.
func TestSplitAdvice_NamesABundledManifestAndHowToSplitIt(t *testing.T) {
	got := splitAdvice([]string{"a.test.ts"}, []string{"a"}, []string{"package.json", "package-lock.json"})
	for _, want := range []string{
		"package.json, package-lock.json",
		"lockfile",
		"git commit -m \"<the dependency change>\" -- package.json package-lock.json",
		"aphrollo gate split-commit -m",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("advice lacks %q:\n%s", want, got)
		}
	}
}

// Real code staged beside the tests is not a manifest bundle.
func TestSplitAdvice_RealSourceIsNotCalledAManifestBundle(t *testing.T) {
	got := splitAdvice([]string{"a_test.go"}, []string{"TestA"}, []string{"a.go"})
	if strings.Contains(got, "lockfile") {
		t.Errorf("no manifest staged, yet the advice blames one:\n%s", got)
	}
}
