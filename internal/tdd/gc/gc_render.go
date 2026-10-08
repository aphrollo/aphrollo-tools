package gc

import (
	"fmt"
	"strconv"
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
	GCKindMutantsTarget:  "mutants build dirs",
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
	for _, k := range []GCKind{GCKindIncremental, GCKindDepsMember, GCKindDepsThirdParty, GCKindMutants,
		GCKindMutantsTarget, GCKindMutantsTemp, GCKindStrayTarget} {
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
	fmt.Fprintf(&b, "%s reclaimable in %d directories — run `aphrollo gate gc` to free it\n", formatBytes(total), len(cands))
	return b.String()
}

// ParseGCAge parses --older-than. Days is the unit a build cache is
// reasoned about in and Go's duration syntax has none, so "3d" is spelled
// out here; everything else falls through to time.ParseDuration. A negative
// or unparseable age is an error, never a silent default -- it would
// otherwise sweep everything.
func ParseGCAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty age")
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			// Zero selects every cache there is, which is a cold rebuild of
			// the workspace dressed up as disk hygiene.
			return 0, fmt.Errorf("invalid age %q (must be greater than zero)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid age %q (must be greater than zero)", s)
	}
	return d, nil
}

// GCOnlyGoCache is the --only name of the Go build cache trim, which is not a
// candidate kind: it trims files inside go's own cache rather than removing
// a directory.
const GCOnlyGoCache = "gocache"

// gcKindNames is the --only vocabulary, one row per candidate kind.
var gcKindNames = []struct {
	name string
	kind GCKind
}{
	{"other", GCKindOther},
	{"incremental", GCKindIncremental},
	{"gate-dir", GCKindGateDir},
	{"orphan-worktree", GCKindOrphanWorktree},
	{"temp-litter", GCKindTempLitter},
	{"mutants", GCKindMutants},
	{"mutants-target", GCKindMutantsTarget},
	{"mutants-temp", GCKindMutantsTemp},
	{"deps-member", GCKindDepsMember},
	{"deps-third-party", GCKindDepsThirdParty},
	{"stray-target", GCKindStrayTarget},
	{"gotmp", GCKindGoTmp},
	{"gate-prmerge", GCKindGatePRMerge},
}

// GCOnlyNames lists every name --only accepts, in the order --help shows them.
func GCOnlyNames() []string {
	names := make([]string, 0, len(gcKindNames)+1)
	for _, k := range gcKindNames {
		names = append(names, k.name)
	}
	return append(names, GCOnlyGoCache)
}

// ParseGCOnly reads a comma-separated --only value into the set of names it
// holds. A name outside GCOnlyNames is an error naming the valid ones.
func ParseGCOnly(value string) (map[string]bool, error) {
	valid := map[string]bool{}
	for _, n := range GCOnlyNames() {
		valid[n] = true
	}
	set := map[string]bool{}
	for _, n := range strings.Split(value, ",") {
		n = strings.TrimSpace(n)
		if !valid[n] {
			return nil, fmt.Errorf("--only: unknown kind %q (valid: %s)", n, strings.Join(GCOnlyNames(), ", "))
		}
		set[n] = true
	}
	return set, nil
}

// FilterGCOnly keeps the candidates whose kind the set names; a nil set keeps
// them all. It is the one place --only narrows a sweep.
func FilterGCOnly(cands []GCCandidate, only map[string]bool) []GCCandidate {
	if only == nil {
		return cands
	}
	var out []GCCandidate
	for _, c := range cands {
		for _, k := range gcKindNames {
			if k.kind == c.Kind && only[k.name] {
				out = append(out, c)
			}
		}
	}
	return out
}
