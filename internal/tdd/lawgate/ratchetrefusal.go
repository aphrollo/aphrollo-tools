package lawgate

import (
	"sort"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// refusalHitCap is how many (law, file) pairs a refusal's event records; the
// rest are in the count. Names only: no file contents ever reach an event.
const refusalHitCap = 10

// refusalDetail is the event detail of a commit refused on the laws: the
// distinct "law|file" pairs of the deny findings, sorted, at most refusalHitCap
// of them, and how many there were. A finding that stands for several files
// (one baseline key held by many) names each; one with no file names the law
// alone. nil when no deny finding is there to name.
func refusalDetail(findings []ratchet.Finding) map[string]string {
	seen := map[string]bool{}
	for _, f := range findings {
		if f.Severity != ratchet.Deny.String() {
			continue
		}
		seen[f.Law+"|"+f.File] = true
		for _, c := range f.Files {
			seen[f.Law+"|"+c.File] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(seen))
	for p := range seen {
		pairs = append(pairs, p)
	}
	sort.Strings(pairs)
	total := len(pairs)
	if total > refusalHitCap {
		pairs = pairs[:refusalHitCap]
	}
	return map[string]string{"hits": strings.Join(pairs, ";"), "hit_count": strconv.Itoa(total)}
}
