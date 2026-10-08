package mutation

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

func provePinRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// A KILLED mutant leaves the proof fail-first accepts for a pin test: the
// killed test, the file, and the git blob of the content that was broken.
func TestRunMutantsProve_AKilledMutantRecordsAPinProofKeyedByTheBrokenContent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := provePinRepo(t)
	blob := strings.TrimSpace(gitOut(root, "hash-object", "widget.go"))

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(root, "widget.go"), Old: "return a + b", New: "return a - b", WantFail: "TestAdd",
	}, RunSuite(precommitTestTimeout), &out, &errb)

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want killed\n%s%s", code, out.String(), errb.String())
	}
	want := []core.PinProof{{Test: "TestAdd", File: "widget.go", Blob: blob}}
	if got := core.PinProofs(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("PinProofs = %v, want %v", got, want)
	}
}

// A mutant that survived proved nothing, so it records nothing.
func TestRunMutantsProve_ASurvivingMutantRecordsNoPinProof(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := provePinRepo(t)
	green := func(Runner, string) SuiteResult { return SuiteResult{Passed: true, Output: "ok\n"} }

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File: filepath.Join(root, "widget.go"), Old: "return a + b", New: "return a - b", WantFail: "TestAdd",
	}, green, &out, &errb)

	if code != ExitMutantsProveSurvived {
		t.Fatalf("exit = %d, want survived\n%s%s", code, out.String(), errb.String())
	}
	if got := core.PinProofs(root); len(got) != 0 {
		t.Fatalf("PinProofs = %v, want none", got)
	}
}
