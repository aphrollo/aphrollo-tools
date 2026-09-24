package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestEnsureSharedSubdir_CreatesWorldWritableSticky(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared", "nested")
	if err := ensureSharedSubdir(dir); err != nil {
		t.Fatalf("ensureSharedSubdir: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatal("ensureSharedSubdir must create a directory")
	}
	if runtime.GOOS != "windows" {
		want := os.FileMode(0o777) | os.ModeSticky
		if fi.Mode()&(os.ModePerm|os.ModeSticky) != want {
			t.Fatalf("mode = %v, want world-writable+sticky (%v)", fi.Mode(), want)
		}
	}
}

func TestEnsureSharedSubdir_IdempotentOnAnExistingDir(t *testing.T) {
	dir := t.TempDir()
	if err := ensureSharedSubdir(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := ensureSharedSubdir(dir); err != nil {
		t.Fatalf("second call on an already-shared dir: %v", err)
	}
}

func TestFitRunes_TruncatesByRuneCountNotByteCount(t *testing.T) {
	if got := fitRunes("short", 10); got != "short" {
		t.Fatalf("a string within the limit must be returned unchanged, got %q", got)
	}
	if got := fitRunes("exact", 5); got != "exact" {
		t.Fatalf("a string exactly at the limit must be unchanged, got %q", got)
	}
	// Each 'é' is two bytes but one rune; cutting by rune count must land
	// between whole runes, never split one.
	in := "ééééé" // 5 runes, 10 bytes
	got := fitRunes(in, 4)
	wantRunes := []rune("éé") // n-3 = 1 real rune kept, plus "..."
	want := string(wantRunes[:1]) + "..."
	if got != want {
		t.Fatalf("fitRunes(%q, 4) = %q, want %q", in, got, want)
	}
	if rc := len([]rune(got)); rc != 4 {
		t.Fatalf("fitRunes must return exactly n runes when cutting, got %d runes in %q", rc, got)
	}
}

func TestFormatElapsedSecs_NeverNegativeAndRoundsToNearestSecond(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-5 * time.Second, "0s"},
		{0, "0s"},
		{1400 * time.Millisecond, "1s"},
		{1500 * time.Millisecond, "2s"},
		{3 * time.Second, "3s"},
	}
	for _, c := range cases {
		if got := formatElapsedSecs(c.d); got != c.want {
			t.Errorf("formatElapsedSecs(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestParseGateLine_ParsesFieldsAndRejectsMalformedLines(t *testing.T) {
	line := "2026-01-01T00:00:00Z precommit /some/repo gate green 1.5s"
	entry, ok := parseGateLine(line)
	if !ok {
		t.Fatal("a well-formed line must parse")
	}
	if entry.Stage != "precommit" || entry.Root != "/some/repo" || entry.Cmd != "gate" || entry.Verdict != "green" || entry.Secs != 1.5 {
		t.Fatalf("parsed entry = %+v", entry)
	}

	if _, ok := parseGateLine("too few fields here"); ok {
		t.Fatal("a line with fewer than 5 fields must not parse")
	}
	if _, ok := parseGateLine("not-a-timestamp precommit /repo gate green 1.0s"); ok {
		t.Fatal("a bad timestamp must not parse")
	}
	if _, ok := parseGateLine("2026-01-01T00:00:00Z precommit /repo gate green notasecond"); ok {
		t.Fatal("a bad duration suffix must not parse")
	}
}

func TestParseGateLine_RecoversAQuotedVerdictWithSpaces(t *testing.T) {
	line := `2026-01-01T00:00:00Z precommit /some/repo gate "inconclusive (fail-open)" 0.0s`
	entry, ok := parseGateLine(line)
	if !ok {
		t.Fatal("a line with a quoted verdict must parse")
	}
	if entry.Verdict != "inconclusive (fail-open)" {
		t.Fatalf("entry.Verdict = %q, want the unwrapped verdict", entry.Verdict)
	}
	if entry.Cmd != "gate" {
		t.Fatalf("entry.Cmd = %q, want %q", entry.Cmd, "gate")
	}
}

func TestAcquirePathLock_UncontendedAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")

	release := acquirePathLock(path)
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("acquirePathLock should create the lock file: %v", err)
	}
	release()

	// After release, the underlying lock must be free again: a fresh
	// TryAcquireFileLock on the same lock path must succeed.
	rel2, ok := TryAcquireFileLock(path + ".lock")
	if !ok {
		t.Fatal("lock should be free after acquirePathLock's release")
	}
	rel2()
}
