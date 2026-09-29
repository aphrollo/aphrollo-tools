package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wipeSrc is production code whose only guard stands between a test's working
// directory and a reset of the checkout around it: a settle run that skips
// the guard acts on wherever `go test` stands, which is the package
// directory of whatever tree it was started in.
const wipeSrc = `package m

import (
	"os"
	"os/exec"
)

// Wiped reports whether it cleaned the checkout above its own directory.
func Wiped(n int) bool {
	if n >= 1 {
		return false
	}
	_ = os.Remove("../notes.txt")
	_ = os.WriteFile("../tracked.txt", []byte("rewritten by a mutant\n"), 0o644)
	_ = exec.Command("git", "read-tree", "-u", "--reset", "HEAD").Run()
	return true
}
`

const wipeTestSrc = `package m

import "testing"

func TestWiped_AGuardedCallDoesNothing(t *testing.T) {
	if Wiped(1) {
		t.Fatal("Wiped ran with its guard satisfied")
	}
}
`

// wipeMutant is gremlins' `>=` to `>` on the guard, a position coverage counts
// but which this settle run is asked about.
func wipeMutant() MutantOutcome {
	line := 1 + strings.Count(wipeSrc[:strings.Index(wipeSrc, "if n >= 1")], "\n")
	return MutantOutcome{File: "m/wipe.go", Line: line, Col: 7, Mutation: "CONDITIONALS_BOUNDARY",
		Name: "m/wipe.go: CONDITIONALS_BOUNDARY", Status: gremlinsNotCovered, NewLine: true}
}

// wipeLane is a committed Go repository holding the work a lane holds between
// commits: an unstaged edit, a staged edit and an untracked file.
func wipeLane(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n")
	write(t, root, "m/wipe.go", wipeSrc)
	write(t, root, "m/wipe_test.go", wipeTestSrc)
	write(t, root, "tracked.txt", "committed\n")
	write(t, root, "staged.txt", "committed\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "staged.txt", "staged edit\n")
	gitDo(t, root, "add", "staged.txt")
	write(t, root, "tracked.txt", "uncommitted edit\n")
	write(t, root, "notes.txt", "untracked work\n")
	return root
}

func TestResolveGapMutants_AMutatedTestThatWipesItsCheckoutLeavesTheLaneIntact(t *testing.T) {
	lane := wipeLane(t)
	out := resolveGapMutants(context.Background(), lane, MutantsConfig{AtMerge: true}, goReachOnce(lane),
		[]MutantOutcome{wipeMutant()}, []int{0}, io.Discard)

	if out[0].Status != "caught" {
		t.Fatalf("status = %q (%s), want caught: the test fails under the mutant", out[0].Status, out[0].Note)
	}
	for rel, want := range map[string]string{
		"tracked.txt": "uncommitted edit\n",
		"staged.txt":  "staged edit\n",
		"notes.txt":   "untracked work\n",
	} {
		if got, err := os.ReadFile(filepath.Join(lane, rel)); err != nil || string(got) != want {
			t.Errorf("lane file %s = %q (err %v), want %q", rel, got, err, want)
		}
	}
	if staged := gitOutT(t, lane, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "staged.txt" {
		t.Errorf("the lane's index lost its staged edit: git diff --cached names %q", staged)
	}
	assertNoSandboxLeft(t, lane)
}
