package shadow

import (
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/store"
)

// LedgerEdit is an edit of the checkout's edit ledger, as far as coverage reads
// it: the file and when the edit was recorded (after the write).
type LedgerEdit struct {
	ID   string
	File string
	At   time.Time
}

// Covered is the kernel's Event.Covered, which the kernel defines as "every
// changed line lies in a function a passing test of the unit executes". Aphrollo
// collects no line coverage, so the adapters define it by what they do hold:
//
//	An edit's unit is covered when a run of the unit was green on a tree that
//	contains the unit's newest edit.
//
// The tree contains the edit when the run read its tree key after the edit was
// recorded, so a run is taken to have started at its record's time less its
// duration (RunVerdict.At is the end of the run, MS how long it took) and the edit
// must not be newer than that. The edit times come from the edit ledger and the
// runs from the tree's verdict in the store.
//
// The answer is conservative where it cannot be known. A unit with no green run of
// its own on the tree it last measured, or no run at all, is not covered, and the
// kernel then reads that as it reads any code the lane has not tested; the run that
// is on its way is the pending phase of the unit's machine, which Covered does not
// guess at. A unit with no edit in the ledger (a new HEAD empties it) is covered by
// a green on the tree it last measured, since a commit keeps the tree.
//
// runs are the runs of the verdict file of the unit's last green tree; edits are the
// ledger's; unitOf names the unit of a file.
func Covered(unit Unit, runs []store.RunVerdict, edits []LedgerEdit, unitOf func(file string) (Unit, bool)) bool {
	var newest time.Time
	for _, e := range edits {
		if u, ok := unitOf(e.File); ok && u.ID == unit.ID && e.At.After(newest) {
			newest = e.At
		}
	}
	for _, r := range runs {
		if r.Result != kernel.VerdictGreen || !runCovers(r.Unit, unit) {
			continue
		}
		start := r.At.Add(-time.Duration(r.MS) * time.Millisecond)
		if !newest.After(start) {
			return true
		}
	}
	return false
}

// runCovers reports whether a stored run (its unit is "<project>|<command>", see
// the run recorder) ran the unit's tests: the same project, and a command that
// either names no package (the whole project) or names the unit's package or a
// pattern holding it. A project-root unit (Python, TypeScript) is covered by any run
// of its project, a pytest of one file or a vitest of one test included: the gate
// holds no per-test or per-symbol knowledge for these languages, so the project is
// the unit and the run's arguments are not read.
func runCovers(runUnit string, u Unit) bool {
	project, cmd, _ := strings.Cut(runUnit, "|")
	if project != u.Project {
		return false
	}
	if u.Kind != unitGoPackage {
		return true
	}
	named := false
	for _, arg := range strings.Fields(cmd) {
		if arg != "." && !strings.HasPrefix(arg, "./") {
			continue
		}
		named = true
		if patternHolds(strings.TrimPrefix(arg, "./"), u.Pkg) {
			return true
		}
	}
	return !named
}

// patternHolds says whether a go package pattern ("...", "dir", "dir/...") holds
// the package directory pkg ("." for the module's root package).
func patternHolds(pat, pkg string) bool {
	switch {
	case pat == "...":
		return true
	case strings.HasSuffix(pat, "/..."):
		base := strings.TrimSuffix(pat, "/...")
		return pkg == base || strings.HasPrefix(pkg, base+"/")
	}
	return pat == pkg || (pat == "." && pkg == ".")
}
