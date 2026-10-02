package workspace

import (
	"strings"
	"testing"
)

func TestGhJobStepCount_ReadsTheCountGhPrintsAndNamesAFailure(t *testing.T) {
	fakeGHPrinting(t, "3", "warning: a new release of gh is available")
	n, err := ghJobStepCount(t.TempDir(), 42)
	if err != nil || n != 3 {
		t.Fatalf("ghJobStepCount = %d, %v; want 3, nil (the stderr warning is not data)", n, err)
	}

	fakeGHPrinting(t, "not a number", "")
	if _, err := ghJobStepCount(t.TempDir(), 42); err == nil {
		t.Error("a non-numeric answer from gh must be an error, not a zero-step job")
	}
}

func TestGhJobStepCount_AnUnreachableGhIsAnErrorNamingTheJob(t *testing.T) {
	// No fake on PATH: this package's TestMain makes the real gh unavailable.
	_, err := ghJobStepCount(t.TempDir(), 7)
	if err == nil || !strings.Contains(err.Error(), "actions/jobs/7") {
		t.Fatalf("err = %v, want one naming actions/jobs/7", err)
	}
}
