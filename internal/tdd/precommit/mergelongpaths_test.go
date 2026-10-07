package precommit

import (
	"fmt"
	"strings"
	"testing"
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
	// cmd.exe refuses a command line past 8191 characters; the literal is the
	// OS's limit, not the gate's own budget, so a budget raised past it fails here.
	const cmdExeLimit = 8191
	covered := map[string]bool{}
	whole := false
	for _, r := range runs {
		line := r.Cmd + " " + strings.Join(r.Args, " ")
		if len(line) > cmdExeLimit {
			t.Errorf("a command line of %d chars, past cmd.exe's %d: %.100s…", len(line), cmdExeLimit, line)
		}
		for _, a := range r.Args {
			if a == "./..." {
				whole = true
			}
			covered[a] = true
		}
	}
	// What the merge brings is judged either by the whole-module fallback or by
	// batches that between them name every package: never a dropped tail.
	if !whole {
		for i := range 400 {
			if p := fmt.Sprintf("./internal/storefront-checkout/components/payment-methods/widget%03d", i); !covered[p] {
				t.Fatalf("package %s was dropped by the batching (%d runs)", p, len(runs))
			}
		}
	}
}
