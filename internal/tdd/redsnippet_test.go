package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildLongNextestFailureOutput builds a nextest-shaped transcript that
// exceeds maxSnippet with a wall of PASS lines FIRST and the actual failure
// detail -- the FAIL summary line plus its panic/assertion detail -- at the
// END, exactly the shape a real nextest run produces (and cargo test's own
// "FAILED" detail dump, which also accumulates at the end). Before this
// task, PostEdit's head-only snippet() showed the green PASS lines and
// truncated before ever reaching the FAIL line, matching the reported bug:
// "hook reports red/no-delta but truncates before naming the test".
func buildLongNextestFailureOutput(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "     PASS [   0.0%ds] widget::tests test_case_%03d\n", i%9, i)
	}
	b.WriteString("     FAIL [   0.1s] server::integration wall::t_x\n")
	b.WriteString("\n")
	b.WriteString("--- STDOUT:              server::integration wall::t_x ---\n")
	b.WriteString("thread 'wall::t_x' panicked at src/wall.rs:42:9:\n")
	b.WriteString("assertion `left == right` failed\n")
	b.WriteString("  left: 7.5\n")
	b.WriteString(" right: 8.0\n")
	out := b.String()
	if len(out) <= maxSnippet {
		t.Fatalf("fixture must exceed maxSnippet (%d) to actually exercise truncation, got %d", maxSnippet, len(out))
	}
	return out
}

// buildLongCompileErrorOutput builds a Rust compile-error transcript with the
// actual error line FIRST and irrelevant diagnostic padding after it, well
// past maxSnippet -- the opposite shape from a test failure: compiler
// output puts the actionable line near the START, not the end, so a
// tail-only snippet would lose it just as badly as head-only loses a test
// failure's detail.
func buildLongCompileErrorOutput(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("error[E0425]: cannot find function `widget` in this scope\n")
	b.WriteString(" --> src/lib.rs:10:5\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "note: additional diagnostic context line %04d padding the output\n", i)
	}
	out := b.String()
	if len(out) <= maxSnippet {
		t.Fatalf("fixture must exceed maxSnippet (%d), got %d", maxSnippet, len(out))
	}
	return out
}

// TestPostEdit_Red_LongNextestFailure_TailSnippetNamesFailureAndLogsFullOutput
// pins requirements (1)+(2): when ExtractFailingTests finds a real failing
// test name, redSummary must pick the TAIL of the output (where nextest/
// cargo actually put the failure detail), name that test as "first
// failure:", and point at a full-output log file whose content is the
// complete, untruncated RED output.
func TestPostEdit_Red_LongNextestFailure_TailSnippetNamesFailureAndLogsFullOutput(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")

	output := buildLongNextestFailureOutput(t)
	got := PostEdit(postPayload("Edit", src), fakeRun(false, output))

	if !strings.Contains(got, "first failure: wall::t_x") {
		t.Fatalf("expected the failing test to be named, got:\n%s", got)
	}
	if !strings.Contains(got, "server::integration wall::t_x") {
		t.Fatalf("expected the snippet body to include the TAIL FAIL line (not truncated away), got:\n%s", got)
	}
	if !strings.Contains(got, "assertion `left == right` failed") {
		t.Fatalf("expected the snippet body to include the failure detail after the FAIL line, got:\n%s", got)
	}

	logPath := filepath.Join(cfg, "gate-state", "postedit-red.log")
	if !strings.Contains(got, logPath) {
		t.Fatalf("expected the advisory to name the full-output log path %s, got:\n%s", logPath, got)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("postedit-red.log not written: %v", err)
	}
	if string(data) != output {
		t.Fatalf("log file content must equal the full RED output verbatim, got %d bytes want %d", len(data), len(output))
	}
}

// TestPostEdit_Red_CompileError_HeadSnippetKeepsErrorLine pins requirement
// (1)'s other branch: when ExtractFailingTests finds NO failing test name
// (a compile error, not a test failure), redSummary must keep the HEAD
// snippet -- the compiler's actual error line lives at the START of the
// output, not the end.
func TestPostEdit_Red_CompileError_HeadSnippetKeepsErrorLine(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")

	output := buildLongCompileErrorOutput(t)
	if len(ExtractFailingTests(output)) != 0 {
		t.Fatalf("fixture precondition: a compile error must parse NO failing test names")
	}

	got := PostEdit(postPayload("Edit", src), fakeRun(false, output))

	if !strings.Contains(got, "error[E0425]: cannot find function `widget` in this scope") {
		t.Fatalf("expected the HEAD snippet to keep the compile error line, got:\n%s", got)
	}
	if strings.Contains(got, "first failure:") {
		t.Fatalf("a compile error has no failing TEST name to report, got:\n%s", got)
	}

	logPath := filepath.Join(cfg, "gate-state", "postedit-red.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("postedit-red.log not written: %v", err)
	}
	if string(data) != output {
		t.Fatalf("log file content must equal the full RED output verbatim")
	}
}

// TestPassAdvisory_NoDelta_PrefersOwnParsedNameOverPrevFailing pins
// requirement (3)'s primary rule: when the current no-delta run's OWN
// output already names a failing test, that name wins -- the
// previously-recorded set is only a FALLBACK, never a competing source.
func TestPassAdvisory_NoDelta_PrefersOwnParsedNameOverPrevFailing(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	got := passAdvisory(r, "/proj", NoDelta, "--- FAIL: TestBaz\n", 0, []string{"TestFoo"})
	if !strings.Contains(got, "first failure: TestBaz") {
		t.Fatalf("expected the current run's own parsed name, got: %s", got)
	}
	if strings.Contains(got, "still failing:") {
		t.Fatalf("must not ALSO show the fallback when the current output already names one, got: %s", got)
	}
}

// TestPassAdvisory_NoDelta_FallsBackToPrevFailing_WhenCurrentParsesNoName
// pins requirement (3)'s defensive fallback: ClassifyOutcome's own guard
// (noNewFailures rejects an empty current-failing set) makes a GENUINE
// no-delta always carry at least one parsed name today, so this exercises
// the fallback directly at passAdvisory's level rather than trying to
// contrive an unreachable end-to-end case -- if that classify.go guard
// ever loosens, the advisory must still name SOMETHING rather than going
// silent about which test is still red.
func TestPassAdvisory_NoDelta_FallsBackToPrevFailing_WhenCurrentParsesNoName(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	got := passAdvisory(r, "/proj", NoDelta, "some output with no parseable failing names", 0, []string{"TestFoo", "TestBar"})
	if !strings.Contains(got, "still failing: TestFoo") {
		t.Fatalf("expected the still-failing fallback naming the FIRST previously-recorded failure, got: %s", got)
	}
}

// TestPassAdvisory_Green_NeverShowsStillFailingHint guards the scope limit:
// the still-failing hint is a no-delta-only concept -- a GREEN run must
// never show it even if prevFailing is non-empty (the whole point of green
// is that nothing is failing anymore).
func TestPassAdvisory_Green_NeverShowsStillFailingHint(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	got := passAdvisory(r, "/proj", Green, "ok\nPASS", 0, []string{"TestFoo"})
	if strings.Contains(got, "still failing:") || strings.Contains(got, "first failure:") {
		t.Fatalf("a green run must never show a failing-test hint, got: %s", got)
	}
}
