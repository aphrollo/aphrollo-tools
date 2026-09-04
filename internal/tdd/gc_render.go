package tdd

import (
	"fmt"
	"strings"
	"time"
)

// How a GC scan renders itself: tier names, the per-tier totals block, the
// report, and the two unit formatters it needs. Split from gc.go, which
// scans and applies — this file only turns findings into text.

var gcTierNames = map[GCKind]string{
	GCKindIncremental:    "incremental caches",
	GCKindDepsMember:     "workspace artifacts",
	GCKindDepsThirdParty: "third-party artifacts",
	GCKindMutants:        "mutants trees",
	GCKindMutantsTemp:    "mutants temp copies",
	GCKindStrayTarget:    "stray target dirs",
}

func writeTierTotals(b *strings.Builder, cands []GCCandidate) {
	totals := map[GCKind]int64{}
	for _, c := range cands {
		if _, named := gcTierNames[c.Kind]; named {
			totals[c.Kind] += c.Size
		}
	}
	for _, k := range []GCKind{GCKindIncremental, GCKindDepsMember, GCKindDepsThirdParty, GCKindMutants, GCKindMutantsTemp, GCKindStrayTarget} {
		if totals[k] > 0 {
			fmt.Fprintf(b, "  %-22s %9s\n", gcTierNames[k], formatBytes(totals[k]))
		}
	}
}

func RenderGC(cands []GCCandidate, applied bool, freed int64) string {
	if len(cands) == 0 {
		return "aphrollo gate gc: nothing reclaimable\n"
	}
	width := 0
	for _, c := range cands {
		if len(c.Path) > width {
			width = len(c.Path)
		}
	}
	var b strings.Builder
	var total int64
	for _, c := range cands {
		fmt.Fprintf(&b, "%-*s  %9s  %s\n", width, c.Path, formatBytes(c.Size), c.Reason)
		total += c.Size
	}
	if applied {
		// Candidates are not deletions: a run whose target dir was busy
		// deletes nothing, and counting the list read as work that happened.
		// The caller reports what it skipped and refused.
		fmt.Fprintf(&b, "freed %s\n", formatBytes(freed))
		return b.String()
	}
	writeTierTotals(&b, cands)
	fmt.Fprintf(&b, "%s reclaimable in %d directories — run `aphrollo gate gc --apply` to free it\n", formatBytes(total), len(cands))
	return b.String()
}

// ParseGCAge parses --older-than. Days is the unit a build cache is
// reasoned about in and Go's duration syntax has none, so "3d" is spelled
// out here; everything else falls through to time.ParseDuration. A negative
// or unparseable age is an error, never a silent default -- it would
// otherwise sweep everything.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// formatDays renders an idle time in whole days (or hours below one), the
// granularity the age threshold is expressed in.
func formatDays(d time.Duration) string {
	if days := int(d.Hours() / 24); days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
