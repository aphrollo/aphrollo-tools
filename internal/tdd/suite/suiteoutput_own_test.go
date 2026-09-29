package suite

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// These are suite's own tests of suiteoutput.go's guards, reached today only
// through internal/tdd/precommit's and postedit's gate tests.

// Serial: reads the state dir from CLAUDE_CONFIG_DIR, a process-wide env var.
// TestRetainSuiteOutput_ARunWithNoOutputOrNoRootIsNotRetained pins the two
// guards: a verdict that never spawned a runner would overwrite the real run a
// session is about to ask for, and a run with no root has nowhere to belong.
func TestRetainSuiteOutput_ARunWithNoOutputOrNoRootIsNotRetained(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()

	retainSuiteOutput("precommit", root, "go test", "green", SuiteResult{})
	retainSuiteOutput("precommit", "", "go test", "green", SuiteResult{Output: "text"})

	entries, _ := os.ReadDir(filepath.Join(cfg, "gate-state"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), suiteOutputPrefix) {
			t.Fatalf("retained %s for a run that had nothing to keep", e.Name())
		}
	}
}

// Serial: reads the state dir from CLAUDE_CONFIG_DIR and resets the once-only
// warning, both process-wide.
// TestRetainSuiteOutput_AnUnwritableStoreWarnsOnceAndKeepsNothing pins the
// failure arm: the decision is untouched, the failure is said (once), and no
// record is written.
func TestRetainSuiteOutput_AnUnwritableStoreWarnsOnceAndKeepsNothing(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", blocker)
	suiteOutputWarnOnce = sync.Once{}
	t.Cleanup(func() { suiteOutputWarnOnce = sync.Once{} })

	stderr := tddtest.CaptureStderr(t, func() {
		retainSuiteOutput("precommit", t.TempDir(), "go test", "green", SuiteResult{Output: "text"})
		retainSuiteOutput("precommit", t.TempDir(), "go test", "green", SuiteResult{Output: "text"})
	})
	if got := strings.Count(stderr, "run output is not being kept"); got != 1 {
		t.Fatalf("the warning appeared %d times in %q, want exactly once", got, stderr)
	}
}

// Serial: reads the state dir from HOME and CLAUDE_CONFIG_DIR, process-wide env vars.
// TestSuiteOutput_WithNoStateDirThereIsNoPathAndNoRecord pins the no-store
// arms: no path for a root, an error writing a record, and an error reading one.
func TestSuiteOutput_WithNoStateDirThereIsNoPathAndNoRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if got := StateDir(); got != "" {
		t.Skipf("this box resolves a state dir (%q) with HOME unset", got) // skip-ok: an environment probe, not a disabled assertion — the arm needs a box that resolves none
	}
	if got := suiteOutputPath("/some/root"); got != "" {
		t.Errorf("suiteOutputPath = %q, want none", got)
	}
	if err := writeSuiteOutputRecord(suiteOutputRecord{Root: "/some/root"}); err == nil {
		t.Error("writing a record with no state dir must be an error")
	}
	if _, err := RetainedSuiteOutput("/some/root"); err == nil {
		t.Error("reading a record with no state dir must be an error")
	}
}

// TestSuiteOutputPath_NoRootHasNoPath pins the empty-root guard.
func TestSuiteOutputPath_NoRootHasNoPath(t *testing.T) {
	t.Parallel()
	if got := suiteOutputPath(""); got != "" {
		t.Fatalf("suiteOutputPath(\"\") = %q, want none", got)
	}
}

// TestRustMountsAsTest_AMountOfAnotherFileIsNotThisFile pins the loop's end:
// a #[cfg(test)] #[path] mount that names a different file does not make the
// target a test module.
func TestRustMountsAsTest_AMountOfAnotherFileIsNotThisFile(t *testing.T) {
	t.Parallel()
	src := "#[cfg(test)]\n#[path = \"tests/wear.rs\"]\nmod wear;\n"
	if rustMountsAsTest("/x/src/lib.rs", src, "/x/src/tests/other.rs") {
		t.Fatal("a mount of wear.rs must not vouch for other.rs")
	}
	if !rustMountsAsTest("/x/src/lib.rs", src, "/x/src/tests/wear.rs") {
		t.Fatal("the mounted file itself is a test module")
	}
}

// TestStaleArtifactHint_NoDiagnosticIsSilence pins the empty-symbols arm.
func TestStaleArtifactHint_NoDiagnosticIsSilence(t *testing.T) {
	t.Parallel()
	if got := staleArtifactHint(t.TempDir(), "all tests passed\n"); got != "" {
		t.Fatalf("hint = %q, want none", got)
	}
}
