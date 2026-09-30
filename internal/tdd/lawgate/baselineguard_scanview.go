package lawgate

import (
	"fmt"
	"sort"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// scanViewMigration judges a staged baseline that carries the scan-view stamp
// its parents lack. The stamp is what moves a law onto the current lexers, and
// the rows that come with it are what those lexers read and the old ones could
// not, so it is admitted only when the staged baseline EQUALS its own
// recomputation: the legacy rows and the staged tree, read by the current
// lexers, exactly as a tightening `ratchet check` writes them. A hand-made
// stamp, an extra row, or a row left out is not that text, and refusal is one
// line saying which. migrated is true for an admitted migration; refusal is
// empty when the baseline is not a migration at all (no stamp, or a parent
// already stamped), leaving the ordinary raise rules to judge it.
func scanViewMigration(repoRoot, rel string, parents []baselineParent, after string) (migrated bool, refusal string) {
	stamped := ratchet.ScanViewOf(after)
	if stamped < 2 {
		return false, ""
	}
	for _, p := range parents {
		if ratchet.ScanViewOf(p.text) >= stamped {
			return false, ""
		}
	}
	opts := ratchet.Options{
		Root:           repoRoot,
		Proposed:       indexOverlay(repoRoot),
		Tracked:        trackedFiles(repoRoot),
		TrackedIgnored: trackedIgnoredFiles(repoRoot),
	}
	var recomputed string
	for _, p := range parents {
		text, ok, err := ratchet.MigratedBaselineText(opts, rel, p.text)
		if err != nil || !ok {
			continue
		}
		if text == after {
			return true, ""
		}
		if recomputed == "" {
			recomputed = text
		}
	}
	return false, fmt.Sprintf("%s carries a scan-view stamp but is not the recomputation from the staged tree under the current lexers: %s",
		rel, scanViewDifference(after, recomputed))
}

// scanViewDifference names the first way the staged baseline departs from its
// recomputation: a row the recomputation lacks, a row it has that the staged
// one leaves out, or, with the same rows, the text itself.
func scanViewDifference(staged, recomputed string) string {
	if recomputed == "" {
		return "no law recomputes it"
	}
	have := baselineCounts(staged)
	want := baselineCounts(recomputed)
	for _, k := range sortedCountKeys(have) {
		if have[k] > want[k] {
			return "extra row " + offendingRow(staged, k)
		}
	}
	for _, k := range sortedCountKeys(want) {
		if want[k] > have[k] {
			return "missing row " + offendingRow(recomputed, k)
		}
	}
	return "text differs from the recomputation"
}

func sortedCountKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
