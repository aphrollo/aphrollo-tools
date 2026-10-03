package postedit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// The edit hook ran `go test ./internal/tdd/...` for one edit to a test file
// beside the subpackages: dozens of suites, minutes, 724 times in one
// gate.log. An edit owes the package that holds the edited file and nothing
// below it.

const goTreeEditPassed = "=== RUN   TestSomething\n--- PASS: TestSomething (0.00s)\nPASS\nok  \texample.com/m/internal/tdd/merge\t0.012s\n"

func TestPostEdit_GoEditsInAPackageTree_ScheduleOnlyThatPackage(t *testing.T) {
	cases := []struct{ name, file, want string }{
		{"test file in a leaf package", "internal/tdd/merge/x_test.go", "go test ./internal/tdd/merge"},
		{"source file in a leaf package", "internal/tdd/merge/x.go", "go test ./internal/tdd/merge"},
		{"test file beside its subpackages", "internal/tdd/session_test.go", "go test ./internal/tdd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tddtest.VerdictWordTmp(t)
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			root := mkGoModule(t)
			write(t, root, "internal/tdd/session.go", "package tdd\n")
			write(t, root, "internal/tdd/merge/x.go", "package merge\n")
			write(t, root, c.file, "package x\n")
			spawned := scriptedPhases(t, map[string]scriptedPhase{
				c.want: {out: &PhaseOutcome{ExitCode: 0}, log: goTreeEditPassed},
			})

			PostEdit(postPayload("Edit", root+"/"+c.file), fakeRun(true, "the foreground runner must not be used"))

			if want := []string{c.want}; !reflect.DeepEqual(*spawned, want) {
				t.Fatalf("edit of %s scheduled %q, want exactly %q", c.file, *spawned, want)
			}
			if strings.Contains(strings.Join(*spawned, " "), "...") {
				t.Fatalf("an edit never runs a subtree, scheduled %q", *spawned)
			}
		})
	}
}
