package mutation

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The Go half of #691's class. A src-file mutation is narrowed to `go test
// ./<dir>` — the mutated file's OWN package — so a mutant that only an
// IMPORTER's test constrains comes back green out of a selection that never
// ran the test able to kill it, and the proof called it a SURVIVOR. Same
// defect as the Rust `--lib` selection that cannot reach an integration
// binary, and the same reason it matters: the pre-merge gate refuses an
// unaccepted survivor by name, so a false one blocks correct work over
// evidence that does not exist.
//
// A same-package test cannot fail this way — `go test ./<dir>` runs it — so
// the killing test here lives in another package on purpose.
//
// These run the REAL `go test`, and the real `go list` behind the widening:
// the whole question is which tests a selection actually runs, and a fake
// runner asked that question answers about itself.

// torqueAndItsImporter is a committed two-package module shaped like the
// defect: the mutated function in package torque, whose own test does not
// constrain the mutated line, and package driveline — an importer — whose
// test does.
func torqueAndItsImporter(t *testing.T) (root, file string) {
	t.Helper()
	root = makeGoRepo(t)
	write(t, root, filepath.FromSlash("torque/torque.go"),
		"package torque\n\n// Split hands a locked differential's input torque to both half-shafts.\n"+
			"func Split(input float64) (float64, float64) {\n\thalf := input / 2.0\n\treturn half, half\n}\n")
	write(t, root, filepath.FromSlash("torque/torque_test.go"),
		"package torque\n\nimport (\n\t\"math\"\n\t\"testing\"\n)\n\n"+
			"func TestSplitReturnsAFinitePair(t *testing.T) {\n\tleft, right := Split(400.0)\n"+
			"\tif math.IsInf(left, 0) || math.IsNaN(right) {\n\t\tt.Fatal(\"not finite\")\n\t}\n}\n")
	write(t, root, filepath.FromSlash("driveline/driveline.go"),
		"package driveline\n\nimport \"example.com/m/torque\"\n\n"+
			"// Delivered is the torque that reaches the road through both half-shafts.\n"+
			"func Delivered(input float64) float64 {\n\tleft, right := torque.Split(input)\n\treturn left + right\n}\n")
	write(t, root, filepath.FromSlash("driveline/driveline_test.go"),
		"package driveline\n\nimport \"testing\"\n\n"+
			"func "+killingTestIsInAnImporter+"(t *testing.T) {\n\tif got := Delivered(400.0); got != 400.0 {\n"+
			"\t\tt.Fatalf(\"Delivered(400) = %v, want 400 (a locked differential conserves torque)\", got)\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root, filepath.Join(root, "torque", "torque.go")
}

// killingTestIsInAnImporter names the test that constrains the mutated line,
// in the package that imports it rather than in the package that owns it.
const killingTestIsInAnImporter = "TestDeliveredConservesTheInputTorque"

// proveTorqueSplit runs the whole proof over that module, with the real
// runner: `go test ./torque` really is green under the mutation and
// `go test ./driveline ./torque` really is red.
func proveTorqueSplit(t *testing.T, root, file string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     file,
		Old:      "input / 2.0",
		New:      "input / 4.0",
		WantFail: killingTestIsInAnImporter,
	}, RunSuite(precommitTestTimeout), &out, &errb)
	return code, out.String() + errb.String()
}

// The defect: a mutant the module's tests DO kill, reported as a survivor
// because the selection stopped at the mutated file's own package.
func TestRunMutantsProve_NeverCallsASurvivorOnASelectionThatStopsAtTheMutatedPackage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, file := torqueAndItsImporter(t)

	code, report := proveTorqueSplit(t, root, file)

	if code == ExitMutantsProveSurvived {
		t.Fatalf("a mutant the importing package's test DOES kill was reported as a survivor:\n%s", report)
	}
	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d):\n%s", code, ExitMutantsProveKilled, report)
	}
	if !strings.Contains(report, killingTestIsInAnImporter) {
		t.Fatalf("the verdict never names the importer's test that killed the mutant:\n%s", report)
	}
}

// The trap of a two-phase verdict: the narrow phase ran first and came back
// green, so the verdict, the names read out of it and the run retained for
// `aphrollo gate output` all have to be the WIDENED run's, or the proof is
// back to reporting evidence that could not have reached the killing test.
//
// Asserted on whole lines. The widened command contains the narrow one's
// package as a substring, so a substring test would read the narrow header as
// the widened one and pass over the confusion it is here to catch.
func TestRunMutantsProve_RecordsTheWidenedGoRunNotTheMutatedPackageAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, file := torqueAndItsImporter(t)

	code, report := proveTorqueSplit(t, root, file)

	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("RetainedSuiteOutput after a widened proof (exit %d): %v\n%s", code, err, report)
	}
	// Sorted, so the command is the same bytes on every run.
	if !strings.Contains(got, "command: go test ./driveline ./torque\n") {
		t.Errorf("the retained record does not name the widened selection as the run it holds:\n%s", got)
	}
	if strings.Contains(got, "command: go test ./torque\n") {
		t.Errorf("the narrow run was retained as the proof's record:\n%s", got)
	}
	if !strings.Contains(got, "--- FAIL: "+killingTestIsInAnImporter) {
		t.Errorf("the retained bytes are not the widened run's — they must be the evidence the verdict was read from:\n%s", got)
	}
}

// The discrimination that keeps the widening from swallowing every survivor:
// a package nothing imports has no test anywhere else that could kill its
// mutants, so `go test ./<dir>` already covered the ground and a green run is
// a REAL survivor, reported exactly as before.
func TestRunMutantsProve_AGreenRunOverAPackageNothingImportsIsStillASurvivor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, filepath.FromSlash("torque/torque.go"),
		"package torque\n\nfunc Split(input float64) (float64, float64) {\n\thalf := input / 2.0\n\treturn half, half\n}\n")
	write(t, root, filepath.FromSlash("torque/torque_test.go"),
		"package torque\n\nimport (\n\t\"math\"\n\t\"testing\"\n)\n\n"+
			"func TestSplitReturnsAFinitePair(t *testing.T) {\n\tleft, right := Split(400.0)\n"+
			"\tif math.IsInf(left, 0) || math.IsNaN(right) {\n\t\tt.Fatal(\"not finite\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	code, report := proveTorqueSplit(t, root, filepath.Join(root, "torque", "torque.go"))

	if code != ExitMutantsProveSurvived {
		t.Fatalf("exit = %d, want ExitMutantsProveSurvived (%d):\n%s", code, ExitMutantsProveSurvived, report)
	}
}

// The constraint that outranks the widening itself: when the reach CANNOT be
// established — `go list` is not there, the module does not load, the graph
// comes back unreadable — the proof knows nothing about which tests could
// have killed the mutant, and an untested verdict is not a result. It reports
// the run inconclusive, in the family NoTestsSelected and the timeout
// verdicts belong to, and never a survivor.
func TestRunMutantsProve_AReachItCannotEstablishIsInconclusiveNotASurvivor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, filepath.FromSlash("torque/torque.go"),
		"package torque\n\nfunc Split(input float64) (float64, float64) {\n\thalf := input / 2.0\n\treturn half, half\n}\n")
	write(t, root, filepath.FromSlash("torque/torque_test.go"),
		"package torque\n\nimport (\n\t\"math\"\n\t\"testing\"\n)\n\n"+
			"func TestSplitReturnsAFinitePair(t *testing.T) {\n\tleft, right := Split(400.0)\n"+
			"\tif math.IsInf(left, 0) || math.IsNaN(right) {\n\t\tt.Fatal(\"not finite\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	restore := stubGoTestReach(t, func(string, string) ([]string, error) {
		return nil, errors.New("go list in the module: exit status 1: go.mod: unknown directive")
	})
	defer restore()

	code, report := proveTorqueSplit(t, root, filepath.Join(root, "torque", "torque.go"))

	if code == ExitMutantsProveSurvived {
		t.Fatalf("a proof that could not establish what reaches the mutated package still claimed a survivor:\n%s", report)
	}
	if code != ExitMutantsProveScopeUnknown {
		t.Fatalf("exit = %d, want ExitMutantsProveScopeUnknown (%d):\n%s", code, ExitMutantsProveScopeUnknown, report)
	}
	if !strings.Contains(strings.ToLower(report), "inconclusive") {
		t.Errorf("the verdict never says it is inconclusive:\n%s", report)
	}
}

func stubGoTestReach(t *testing.T, fn func(root, dir string) ([]string, error)) func() {
	t.Helper()
	return SetGoTestReachForTest(fn)
}
