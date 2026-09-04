package tdd

import "testing"

// `mutants-local` answers one question -- does the post-commit hook START a
// run on this box -- and the merge gate borrowed it to answer a different one:
// may this box JUDGE a receipt. That conflation has a cost. A box whose runs
// are measured elsewhere but under the SAME signing key (a Linux clone sharing
// CLAUDE_CONFIG_DIR, which is how this repo earns its receipts) can find and
// verify every receipt it is handed, yet turning the local run off silently
// turned the proof off with it, and every lane merged on nothing.
//
// So the two questions get two keys. `mutants-judge-local` defaults to
// `mutants-local`, leaving a genuine CI repo exactly as it was, and states the
// answer outright when the two differ.
func TestMutationReceiptStage_StillDemandsAReceiptWhenOnlyTheRunIsRemote(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := goReceiptRepo(t, "[aphrollo]\nmutation-receipt = true\nmutants-local = false\nmutants-judge-local = true\n")
	startMerge(t, root, "main", "lane/x")

	got := mutationReceiptStage(root)
	if got == nil || !got.Blocked {
		t.Fatalf("mutationReceiptStage = %+v, want a BLOCK: the run is remote but its receipt is signed with this box's key, so the proof is still owed", got)
	}
}

// TestMutationJudgedLocally_DefaultsToWhereTheRunHappens keeps the new key from
// changing any repo that does not set it: with only `mutants-local` present,
// judging follows it in both directions.
func TestMutationJudgedLocally_DefaultsToWhereTheRunHappens(t *testing.T) {
	for _, tc := range []struct {
		name string
		toml string
		want bool
	}{
		{"run is local", "[aphrollo]\nmutation-receipt = true\nmutants-local = true\n", true},
		{"run is remote", "[aphrollo]\nmutation-receipt = true\nmutants-local = false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := goReceiptRepo(t, tc.toml)
			if got := mutationJudgedLocally(root); got != tc.want {
				t.Errorf("mutationJudgedLocally = %v, want %v — an unset key must not change what the repo already did", got, tc.want)
			}
		})
	}
}

// ...and the override works in the other direction too: a box that DOES run
// the measurement but wants CI to be the judge says so.
func TestMutationJudgedLocally_CanStandDownWhileTheRunStaysLocal(t *testing.T) {
	root := goReceiptRepo(t, "[aphrollo]\nmutation-receipt = true\nmutants-local = true\nmutants-judge-local = false\n")
	if mutationJudgedLocally(root) {
		t.Error("mutants-judge-local = false must stand the gate down even though the run is local")
	}
}
