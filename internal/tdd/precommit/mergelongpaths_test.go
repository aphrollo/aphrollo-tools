package precommit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// #996: a conflict-free merge commit stages every file the merge brings, and the
// pre-merge-commit gate put them all on one command line, which cmd.exe refuses
// past 8191 characters ("The command line is too long"). A lane landing on trunk
// with 400 packages under long paths is that merge; every command the gate
// builds from them must stay inside the budget, whatever it chooses to run.
func TestMechanical_MergeOfFourHundredLongPathsBuildsNoOverlongCommandLine(t *testing.T) {
	t.Parallel()
	lane := map[string]string{}
	for i := range 400 {
		p := fmt.Sprintf("internal/storefront-checkout/components/payment-methods/widget%03d", i)
		lane[p+"/w.go"] = "package w\n\nfunc W() int { return 1 }\n"
		lane[p+"/w_test.go"] = "package w\n\nimport \"testing\"\n\nfunc TestW(t *testing.T) {}\n"
	}
	root, trunk := syncRepo(t, lane, map[string]string{"README.md": "# readme\n"})
	gitDo(t, root, "checkout", "-q", trunk)
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "lane/work")

	runs := mechanicalRuns(t, root)
	if len(runs) == 0 {
		t.Fatal("a merge bringing 400 packages ran nothing")
	}
	for _, r := range runs {
		if line := r.Cmd + " " + strings.Join(r.Args, " "); len(line) > argvbatch.Budget {
			t.Errorf("a command line of %d chars, past the %d-char budget: %.100s…", len(line), argvbatch.Budget, line)
		}
	}
}
