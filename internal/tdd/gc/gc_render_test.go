package gc

import (
	"strings"
	"testing"
	"time"
)

// A tier whose candidates sum to zero bytes must not print a row: a
// `> 0` slipping to `>= 0` would show "stray target dirs   0 B" on every
// scan that never found one, noise the operator would learn to ignore.
func TestWriteTierTotals_omitsTierWhenTotalIsZero(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	writeTierTotals(&b, []GCCandidate{
		{Path: "a", Size: 100, Reason: "r1", Kind: GCKindIncremental},
		{Path: "b", Size: 0, Reason: "r2", Kind: GCKindStrayTarget},
	})
	out := b.String()
	const want = "  incremental caches         100 B\n"
	if out != want {
		t.Fatalf("writeTierTotals output = %q, want %q", out, want)
	}
	if strings.Contains(out, "stray target dirs") {
		t.Fatalf("writeTierTotals output = %q, must omit a zero-byte tier", out)
	}
}

// The path column is padded to the LONGEST path, not the first or the last —
// a shorter path earlier in the slice must still get the full width. This
// pins the exact byte layout so a max-tracking slip (never firing, or firing
// on the wrong side) shows up as a literal mismatch rather than a missing
// substring.
func TestRenderGC_padsShorterPathToLongestPathWidth(t *testing.T) {
	t.Parallel()
	out := RenderGC([]GCCandidate{
		{Path: "a", Size: 100, Reason: "r1", Kind: GCKindIncremental},
		{Path: "bbbb", Size: 200, Reason: "r2", Kind: GCKindIncremental},
	}, false, 0)

	row1 := "a         100 B  r1\n" // "a" padded to width 4, then two spaces, %9s "100 B", two spaces, reason
	row2 := "bbbb      200 B  r2\n" // "bbbb" already width 4, no pad
	tier := "  incremental caches         300 B\n"
	summary := "300 B reclaimable in 2 directories — run `aphrollo gate gc --apply` to free it\n"
	want := row1 + row2 + tier + summary
	if out != want {
		t.Fatalf("RenderGC output =\n%q\nwant\n%q", out, want)
	}
}

// The unit crossover from bytes to kibibytes sits at exactly 1024 — one
// short of it stays "B", exactly at it must already read "KB". A `<`
// slipping to `<=` would keep 1024 itself in the bytes branch.
func TestFormatBytes_crossesToKilobytesAtOneKibibyteBoundary(t *testing.T) {
	t.Parallel()
	if got := formatBytes(1024); got != "1.0 KB" {
		t.Fatalf("formatBytes(1024) = %q, want %q", got, "1.0 KB")
	}
	if got := formatBytes(1023); got != "1023 B" {
		t.Fatalf("formatBytes(1023) = %q, want %q", got, "1023 B")
	}
}

// The exponent walk stops at T (index 3) — "KMGT" has exactly four letters,
// so letting the loop run one more step indexes past the end. A large enough
// input (staying above the 1024 threshold through all three promotions)
// exercises the `exp < 3` ceiling directly rather than the `v >= unit` arm.
func TestFormatBytes_capsExponentAtTebibytesAndNeverIndexesPastIt(t *testing.T) {
	t.Parallel()
	n := int64(1)
	for i := 0; i < 6; i++ {
		n *= 1024 // 1024^6 = 2^60, well within int64
	}
	if got := formatBytes(n); got != "1048576.0 TB" {
		t.Fatalf("formatBytes(1024^6) = %q, want %q", got, "1048576.0 TB")
	}
}

// The day/hour crossover sits at exactly 24 hours — one second short must
// still read in hours, exactly at it must already read "1d". This pins both
// the boundary and the /24 division that decides it.
func TestFormatDays_crossesToDaysAtTwentyFourHourBoundary(t *testing.T) {
	t.Parallel()
	if got := formatDays(24 * time.Hour); got != "1d" {
		t.Fatalf("formatDays(24h) = %q, want %q", got, "1d")
	}
	if got := formatDays(23*time.Hour + 59*time.Minute); got != "23h" {
		t.Fatalf("formatDays(23h59m) = %q, want %q", got, "23h")
	}
}
