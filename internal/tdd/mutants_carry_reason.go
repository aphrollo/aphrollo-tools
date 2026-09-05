package tdd

import (
	"fmt"
	"sort"
)

// A run that re-measures a file nobody touched is the most expensive thing
// this system does, and until now it left no record of WHY. The plan knew —
// it compared a blob and a fence and rejected the carry — and then threw the
// comparison away, so an operator watching a catch-up merge re-measure a
// package could not tell an ordinary first run from a carry that should have
// worked.
//
// The two live reasons have different fixes and must never be confused:
//
//	blob-moved   the file's own content changed. Re-measuring is correct;
//	             this is the lane's own edit.
//	fence-moved  the file is byte-identical and its PACKAGE's test set moved.
//	             The fence is package-wide, so one unrelated test edit
//	             re-measures every mutant in the package — including files the
//	             lane never opened. This is the reason worth watching.
type CarryReason string

const (
	CarryBlobMoved     CarryReason = "blob-moved"
	CarryFenceMoved    CarryReason = "fence-moved"
	CarryNeverMeasured CarryReason = "never-measured"
	// CarryNoIdentity is a stored outcome from a producer that reported no
	// blob or no fence. It cannot be matched against anything, so it is not a
	// carry failure so much as an entry that was never usable.
	CarryNoIdentity CarryReason = "stored-without-identity"
)

// CarrySkip is one mutant that had to be measured again, and why.
type CarrySkip struct {
	File    string
	Package string
	Reason  CarryReason
}

// carryReasonFor names why old could not answer for the current blob and
// fence. It is only called once a carry has already been rejected, so it
// never returns "carried".
func carryReasonFor(old MutantOutcome, found bool, blob, fence string) CarryReason {
	switch {
	case !found:
		return CarryNeverMeasured
	case old.Blob == "" || old.Fence == "":
		return CarryNoIdentity
	case old.Blob != blob:
		return CarryBlobMoved
	default:
		return CarryFenceMoved
	}
}

// CarrySkipSummary folds the skips into one line per FILE. Per-file, not
// per-mutant: a package with 200 mutants would otherwise bury the answer in
// the log meant to give it.
//
// A file with more than one reason reports each, because the mixed case is
// itself the interesting one — some mutants stale for content, others for
// tests, in the same file.
func CarrySkipSummary(skipped []CarrySkip) []string {
	type group struct {
		file, pkg string
		reason    CarryReason
	}
	counts := map[group]int{}
	for _, s := range skipped {
		counts[group{s.File, s.Package, s.Reason}]++
	}
	lines := make([]string, 0, len(counts))
	for g, n := range counts {
		lines = append(lines, fmt.Sprintf("%s (%s): %d mutant(s) re-measured — %s", g.file, g.pkg, n, g.reason))
	}
	// Stable output: the log is read by a person comparing one run to the
	// next, and map order would make two identical runs look different.
	sort.Strings(lines)
	return lines
}
