package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// futureMtimeOwnTarget makes a fresh target dir with one artifact file and
// returns its path, so each test gets its own futureMtimeChecked entry
// (keyed on the absolute target path) and its own on-disk marker.
func futureMtimeOwnTarget(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "artifact.rmeta"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return target
}

// TestScanForFutureMtime_FindsTheFirstFileAfterCutoff pins the walk itself,
// independent of the wipe decision built on top of it: one file's mtime
// after cutoff is a hit, and a directory entry's own mtime must never be
// mistaken for a file's (WalkDir visits directories too).
func TestScanForFutureMtime_FindsTheFirstFileAfterCutoff(t *testing.T) {
	target := t.TempDir()
	stale := filepath.Join(target, "stale.rmeta")
	future := filepath.Join(target, "future.rmeta")
	for _, p := range []string{stale, future} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(stale, now, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	wantMtime := now.Add(2 * time.Hour)
	if err := os.Chtimes(future, now, wantMtime); err != nil {
		t.Fatal(err)
	}

	path, mtime := scanForFutureMtime(target, now)
	if path != future {
		t.Fatalf("scanForFutureMtime found %q, want the future-stamped %q", path, future)
	}
	if !mtime.Equal(wantMtime) {
		t.Fatalf("reported mtime = %s, want %s", mtime, wantMtime)
	}
}

// TestScanForFutureMtime_NothingPastCutoffReportsEmpty is the negative case
// invalidateFutureStampedArtifacts relies on to do nothing when the target
// dir is clean.
func TestScanForFutureMtime_NothingPastCutoffReportsEmpty(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "ordinary.rmeta"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, _ := scanForFutureMtime(target, time.Now()); path != "" {
		t.Fatalf("scanForFutureMtime = %q, want \"\" when nothing is future-stamped", path)
	}
}

// TestFutureMtimeMarker_StoreThenLoadRoundTrips pins the persistence the
// repeat-incident check depends on: a marker written for one target is read
// back with the same WipedAt, and a target with no marker at all reports
// ok=false rather than a zero-value marker that could be mistaken for one
// wiped at the Unix epoch.
func TestFutureMtimeMarker_StoreThenLoadRoundTrips(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := t.TempDir()

	if _, ok := loadFutureMtimeMarker(target); ok {
		t.Fatal("loadFutureMtimeMarker before any store reports ok=true, want false")
	}

	wipedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	storeFutureMtimeMarker(target, wipedAt)

	got, ok := loadFutureMtimeMarker(target)
	if !ok {
		t.Fatal("loadFutureMtimeMarker after a store reports ok=false, want true")
	}
	if !got.WipedAt.Equal(wipedAt) {
		t.Fatalf("loaded WipedAt = %s, want %s", got.WipedAt, wipedAt)
	}
}

// TestInvalidateFutureStampedArtifacts_WipesOnFirstOffense is issue #276's
// fix: a target dir carrying a future-stamped artifact is wiped the first
// time it is seen, and the stderr report names the file and how far ahead
// it was stamped, so an operator reading the log has the fact rather than a
// bare "something was wrong".
func TestInvalidateFutureStampedArtifacts_WipesOnFirstOffense(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := futureMtimeOwnTarget(t)
	future := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(filepath.Join(target, "artifact.rmeta"), future, future); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() { invalidateFutureStampedArtifacts(target) })

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target %q still exists after a first-offense wipe (err=%v), want it removed", target, err)
	}
	if !strings.Contains(out, "wiping it before the build") {
		t.Fatalf("stderr = %q, want it to say the target dir was wiped", out)
	}
	if strings.Contains(out, "not wiping again") {
		t.Fatalf("stderr = %q, a first offense must not claim to be a repeat", out)
	}
}

// TestInvalidateFutureStampedArtifacts_SecondCallOnSameTargetIsANoOp pins
// futureMtimeChecked's own job: nothing else writes into a target dir
// between two stages of the same gate run except this lock's own holders, so
// a SECOND call for the same target in this process must not walk a
// possibly enormous target dir again to re-confirm what the first call
// already answered — it must report nothing at all, even if the first call
// found (and wiped) a real offense.
func TestInvalidateFutureStampedArtifacts_SecondCallOnSameTargetIsANoOp(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := futureMtimeOwnTarget(t)
	future := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(filepath.Join(target, "artifact.rmeta"), future, future); err != nil {
		t.Fatal(err)
	}
	invalidateFutureStampedArtifacts(target) // first call: wipes it, marks target "checked"

	// Recreate the target with ANOTHER future-stamped file: if the second
	// call actually re-scanned, it would find this and either wipe or report
	// again. It must do neither.
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "artifact.rmeta"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(target, "artifact.rmeta"), future, future); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() { invalidateFutureStampedArtifacts(target) })

	if out != "" {
		t.Fatalf("stderr on the second call = %q, want no output — the target was already checked this process", out)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the recreated target was removed by a second call that should have been a no-op: %v", err)
	}
}

// TestInvalidateFutureStampedArtifacts_CleanTargetIsLeftAlone is the
// narrower-than-a-blanket-wipe half of the fix: a target with no
// future-stamped artifact must never be touched, or degrade EVERY run on a
// box with real persistent clock skew (an unsynced VM, an NFS/SMB mount)
// into a full cold rebuild forever.
func TestInvalidateFutureStampedArtifacts_CleanTargetIsLeftAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := futureMtimeOwnTarget(t)

	out := captureStderr(t, func() { invalidateFutureStampedArtifacts(target) })

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("clean target %q was touched: %v", target, err)
	}
	if out != "" {
		t.Fatalf("stderr = %q, want no output for a clean target", out)
	}
}

// TestInvalidateFutureStampedArtifacts_RecentRepairIsNotRepeated is the
// repeat-incident rule: a target already repaired within
// futureMtimeMarkerExpiry that shows a future-stamped artifact again is the
// SAME clock producing the same symptom right after a rebuild, not a fresh
// incident — it must be reported and left alone, never wiped a second time
// paying the full cold-rebuild cost every run.
func TestInvalidateFutureStampedArtifacts_RecentRepairIsNotRepeated(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := futureMtimeOwnTarget(t)
	storeFutureMtimeMarker(target, time.Now().Add(-time.Hour))
	future := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(filepath.Join(target, "artifact.rmeta"), future, future); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() { invalidateFutureStampedArtifacts(target) })

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target %q was wiped on a repeat within the marker's expiry, want it left alone: %v", target, err)
	}
	if !strings.Contains(out, "not wiping again") {
		t.Fatalf("stderr = %q, want it to say a repeat repair is refused", out)
	}
}

// TestInvalidateFutureStampedArtifacts_ExpiredRepairWipesAgain is the other
// half of the same rule: once futureMtimeMarkerExpiry has passed since the
// last repair, this reads as a NEW incident again, so the target is wiped
// once more rather than staying wedged in "no wipe" state forever for an
// operator who never knew the marker existed.
func TestInvalidateFutureStampedArtifacts_ExpiredRepairWipesAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := futureMtimeOwnTarget(t)
	storeFutureMtimeMarker(target, time.Now().Add(-futureMtimeMarkerExpiry-time.Hour))
	future := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(filepath.Join(target, "artifact.rmeta"), future, future); err != nil {
		t.Fatal(err)
	}

	invalidateFutureStampedArtifacts(target)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target %q still exists after its marker expired (err=%v), want it wiped again", target, err)
	}
}

// TestInvalidateFutureStampedArtifacts_LargeSkewWarnsOfALikelyBadClock pins
// the wording split: a skew past futureMtimeLargeSkew still gets the
// automatic first repair, but the log adds the warning that a REPEAT within
// a week will not be, priming whoever reads it for the repeat-no-wipe line
// if the clock really is wrong.
func TestInvalidateFutureStampedArtifacts_LargeSkewWarnsOfALikelyBadClock(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := futureMtimeOwnTarget(t)
	future := time.Now().Add(futureMtimeLargeSkew + time.Hour)
	if err := os.Chtimes(filepath.Join(target, "artifact.rmeta"), future, future); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() { invalidateFutureStampedArtifacts(target) })

	if !strings.Contains(out, "a repeat within a week will not be") {
		t.Fatalf("stderr = %q, want the large-skew warning about a future repeat", out)
	}
}
