package postedit

import (
	"strings"
	"testing"
)

// TestPostEdit_ForegroundPhaseReportsItsOutput pins the bug that made the
// whole deferral path silent: startAndWait discarded the job the spawner
// returned (the one carrying Log/Result), so every phase that finished in
// the FOREGROUND read its output from a job with an empty Log — a green run
// came back as "writing-test" (zero tests seen) and a red one named no test
// and carried no snippet.
func TestPostEdit_ForegroundPhaseReportsItsOutput(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	done := &PhaseOutcome{ExitCode: 0, Seconds: 1}
	fakePhases(t, done, done)

	got := PostEdit(postPayload("Edit", root+"/src/widget.rs"), fakeRun(true, "ok"))

	if !strings.Contains(got, "1 passed") {
		t.Fatalf("advisory = %q, want the phase's own '1 passed' output", got)
	}
	if strings.Contains(got, "writing-test") {
		t.Fatalf("advisory = %q — an empty log makes a real run look like scaffolding", got)
	}
}
