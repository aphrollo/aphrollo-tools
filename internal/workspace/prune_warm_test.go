package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The gate keeps one warm checkout per repo and purpose so Go, lint and tsc
// caches keyed by path stay warm. Its holder is dead between merges by
// design, so a dead holder must not make prune remove it; gate gc reaps it
// once it has sat idle (internal/tdd/gc).
func TestPrune_WarmGateCheckout_DeadHolderIsKept(t *testing.T) {
	for _, name := range []string{"gate-prmerge-warm", "gate-prmerge-localci"} {
		repo := initRepo(t)
		wt := filepath.Join(t.TempDir(), name)
		addDetachedWorktree(t, repo, wt)
		writeGatePRMergeHolder(t, wt, deadPidForTest(t))

		p, err := PrunePlan(repo)
		if err != nil {
			t.Fatalf("PrunePlan: %v", err)
		}
		var out, errb bytes.Buffer
		if err := p.Run(true, &out, &errb); err != nil {
			t.Fatalf("Run: %v\n%s", err, errb.String())
		}
		if _, err := os.Stat(wt); err != nil {
			t.Errorf("%s: a warm gate checkout must survive prune: %v", name, err)
		}
	}
}
