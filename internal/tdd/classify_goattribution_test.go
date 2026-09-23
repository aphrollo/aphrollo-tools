package tdd

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A passing multi-package Go run in which ONE package has no test file used
// to read as writing-test: zeroTestsRe matched that package's "[no test
// files]" line anywhere in the output, however many tests the other packages
// ran. writing-test means "no test ran at all", so the attribution has to be
// per package — go test states per package whether it ran a test, in its
// -json events and in its own summary lines.

// mkGoTreeWithTestlessSubpackage writes a module whose internal/a package
// has one real test and whose internal/a/sub package has none — the shape a
// test edit's `go test ./internal/a/...` runs.
func mkGoTreeWithTestlessSubpackage(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n")
	write(t, root, "internal/a/a.go", "package a\n\nfunc One() int { return 1 }\n")
	write(t, root, "internal/a/a_test.go",
		"package a\n\nimport \"testing\"\n\nfunc TestOneIsOne(t *testing.T) {\n\tif One() != 1 {\n\t\tt.Fatal(\"One() != 1\")\n\t}\n}\n")
	write(t, root, "internal/a/sub/sub.go", "package sub\n\nfunc Two() int { return 2 }\n")
	return root
}

// TestPostEdit_GoRunWithATestlessPackage_IsGreenWithItsRealCount runs real
// `go test -json` on the foreground path: one package ran a test, one has no
// test file. The run is green, with the one test it ran.
func TestPostEdit_GoRunWithATestlessPackage_IsGreenWithItsRealCount(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever go is installed.
		t.Skip("go not on PATH")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkGoTreeWithTestlessSubpackage(t)

	got := PostEdit(postPayload("Edit", root+"/internal/a/a_test.go"), RunSuite(2*time.Minute))

	if !strings.Contains(got, "go test ./internal/a/...") {
		t.Fatalf("setup: want the test edit's package tree run, got: %s", got)
	}
	if strings.Contains(got, string(WritingTest)) {
		t.Fatalf("a run in which a package ran a real test is not writing-test, got: %s", got)
	}
	if !strings.Contains(got, "green (1 passed") {
		t.Fatalf("want green with the one test that ran, got: %s", got)
	}
}

// TestPostEdit_DeferredGoRunWithATestlessPackage_IsGreen is the same run on
// the deferred path, which reads go test's plain per-package summary lines:
// "ok" for a package that ran tests, "?" for one with no test file.
func TestPostEdit_DeferredGoRunWithATestlessPackage_IsGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkGoTreeWithTestlessSubpackage(t)
	scriptedPhases(t, map[string]scriptedPhase{
		"go test ./internal/a/...": {out: &PhaseOutcome{ExitCode: 0},
			log: "ok  \texample.com/m/internal/a\t0.004s\n?   \texample.com/m/internal/a/sub\t[no test files]\n"},
	})

	got := PostEdit(postPayload("Edit", root+"/internal/a/a_test.go"), fakeRun(true, "the foreground runner must not be used"))

	if strings.Contains(got, string(WritingTest)) || !strings.Contains(got, "green") {
		t.Fatalf("a run in which a package ran its tests is green, got: %s", got)
	}
}

// TestClassifyRunOutcome_GoRunInWhichNoPackageRanATest_IsWritingTest pins
// the other side: writing-test stays exactly when NO package ran a test.
func TestClassifyRunOutcome_GoRunInWhichNoPackageRanATest_IsWritingTest(t *testing.T) {
	json := `{"Action":"start","Package":"example.com/m/internal/a"}
{"Action":"output","Package":"example.com/m/internal/a","Output":"testing: warning: no tests to run\n"}
{"Action":"output","Package":"example.com/m/internal/a","Output":"ok  \texample.com/m/internal/a\t0.002s [no tests to run]\n"}
{"Action":"pass","Package":"example.com/m/internal/a","Elapsed":0.002}
{"Action":"output","Package":"example.com/m/internal/a/sub","Output":"?   \texample.com/m/internal/a/sub\t[no test files]\n"}
{"Action":"skip","Package":"example.com/m/internal/a/sub","Elapsed":0}
`
	human, raw, ok := renderGoTestJSON(json)
	if !ok {
		t.Fatal("setup: the fixture stream must decode")
	}
	res := SuiteResult{Passed: true, Output: human, GoTestJSON: raw}
	if got := classifyRunOutcome(Runner{Cmd: "go", Args: []string{"test", "./internal/a/..."}}, "", res, nil); got != WritingTest {
		t.Fatalf("classifyRunOutcome = %q, want %q for a run no package ran a test in", got, WritingTest)
	}
}
