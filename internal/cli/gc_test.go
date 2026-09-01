package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mkAgedFile writes a file (with parents) and back-dates it, so a test can
// build a target dir that looks idle.
func mkAgedFile(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// TestRunTDDGC_DryRunListsAndDeletesNothing pins the command's default: a
// bare `aphrollo tdd gc` is a REPORT. The directory it named must still be
// there afterwards — a disk sweep that deletes without being asked is the
// one bug this whole feature cannot have.
func TestRunTDDGC_DryRunListsAndDeletesNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	code := runTDDGC([]string{"--repo", repo}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "stale-1a2b") || !strings.Contains(out, "--apply") {
		t.Fatalf("dry run must name the candidate and the apply command, got:\n%s", out)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("a dry run must delete nothing")
	}
}

// TestRunTDDGC_ApplyDeletesAndReports pins --apply: the candidates go, and
// the operator is told what was freed.
func TestRunTDDGC_ApplyDeletesAndReports(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	if code := runTDDGC([]string{"--repo", repo, "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("--apply must actually delete the candidate")
	}
	if !strings.Contains(stdout.String(), "freed") {
		t.Fatalf("--apply must report what it freed, got:\n%s", stdout.String())
	}
}

// TestRunTDDGC_OlderThanBoundsWhatQualifies pins --older-than: a cache
// younger than the threshold is not a candidate, and the default is the
// documented 3 days.
func TestRunTDDGC_OlderThanBoundsWhatQualifies(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	fiveDays := filepath.Join(repo, "target", "debug", "incremental", "five-days")
	mkAgedFile(t, filepath.Join(fiveDays, "dep-graph.bin"), "01234", 5*24*time.Hour)

	var stdout, stderr bytes.Buffer
	runTDDGC([]string{"--repo", repo}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "five-days") {
		t.Fatalf("the default 3d threshold must reclaim a 5-day-old cache, got:\n%s", stdout.String())
	}

	stdout.Reset()
	runTDDGC([]string{"--repo", repo, "--older-than", "14d"}, &stdout, &stderr)
	if strings.Contains(stdout.String(), "five-days") {
		t.Fatalf("--older-than 14d must spare a 5-day-old cache, got:\n%s", stdout.String())
	}
}

// TestRunTDDGC_RejectsAnUnparseableAge pins the one hard failure: a
// mistyped age must stop the command, never fall back to a default that
// sweeps more than the operator asked for.
func TestRunTDDGC_RejectsAnUnparseableAge(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runTDDGC([]string{"--older-than", "soon", "--apply"}, &stdout, &stderr); code == 0 {
		t.Fatal("an unparseable --older-than must fail, not guess")
	}
	if !strings.Contains(stderr.String(), "soon") {
		t.Fatalf("the error must name the bad value, got: %s", stderr.String())
	}
}

// TestRunTDDGC_QuietApplyIsSilentButStillRecordsTheSweep pins the
// background sweep's contract: --quiet prints nothing (it is detached, with
// nowhere to print), and the result is left in the state dir for the next
// session start to surface.
func TestRunTDDGC_QuietApplyIsSilentButStillRecordsTheSweep(t *testing.T) {
	state := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", state)
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	if code := runTDDGC([]string{"--repo", repo, "--apply", "--quiet"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("--quiet must print nothing, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(state, "tdd-state", "gc-last-report.json")); err != nil {
		t.Fatal("a quiet sweep must still record its result for the next session to report")
	}
}
