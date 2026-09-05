package tdd

import (
	"strings"
	"testing"
)

// ratchet: test_removed TestPipeline_SavesTheMutationOutcomeStoreEvenWhenTheMutantsStepFails: renamed
// to TestNightlyMutants_SavesTheMutationOutcomeStoreEvenWhenTheMutantsStepFails below and retargeted
// at nightly-mutants.yml, the job's new home since issue #334 moved it off pipeline.yml's per-push path.

// The built-in Post-if save on a single `actions/cache@...` step does not
// reliably fire when the step it wraps around FAILS — observed on PR #150's
// first push (31 mutants measured, 5 unaccepted survivors): no "Cache saved
// with key: ..." line ever appeared in that job's log, so the very run whose
// measurements a fix-up push most wants to reuse was the one that left
// nothing behind, and the next push re-measured the whole diff from scratch.
// The fix is an explicit `actions/cache/save` step that runs unconditionally.
func TestNightlyMutants_SavesTheMutationOutcomeStoreEvenWhenTheMutantsStepFails(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-mutants.yml")
	for want, why := range map[string]string{
		"actions/cache/restore@": "the store must be restored through the split action, not the combined one whose save half is unreliable on failure",
		"actions/cache/save@":    "the store must be saved through an explicit step, not relied on to run as the combined action's post-step",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("%s (looked for %q in .github/workflows/nightly-mutants.yml)", why, want)
		}
	}
	restoreIdx := strings.Index(wf, "actions/cache/restore@")
	saveIdx := strings.Index(wf, "actions/cache/save@")
	runIdx := strings.Index(wf, "gate mutants go --diff")
	if restoreIdx < 0 || runIdx < 0 || saveIdx < 0 {
		t.Fatal("could not locate all three steps to check their order")
	}
	if restoreIdx >= runIdx || runIdx >= saveIdx {
		t.Fatalf("step order = restore@%d run@%d save@%d, want restore, then the run, then save", restoreIdx, runIdx, saveIdx)
	}
	// The save step's own `if:` must not be gated on the run's success —
	// always() is what makes it run when the mutants step fails.
	stepStart := strings.LastIndex(wf[:saveIdx], "\n      - name:")
	stepEnd := strings.Index(wf[saveIdx:], "\n      - ")
	if stepEnd < 0 {
		stepEnd = len(wf) - saveIdx
	}
	saveStepBlock := wf[stepStart : saveIdx+stepEnd]
	if !strings.Contains(saveStepBlock, "always()") {
		t.Errorf("the save step must carry always() in its if:, got:\n%s", saveStepBlock)
	}
}
