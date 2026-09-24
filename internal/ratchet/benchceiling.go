package ratchet

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ceilingHits dispatches a whole-tree ceiling law to its reader: the two
// numeric-ceiling kinds share one baseline mechanism (a Hit's Weight is the
// measured value, only ever allowed to fall) but read different file
// shapes — generated JSON versus `go test -bench -benchmem` text.
func ceilingHits(view treeView, law Law, requireData bool, targetDir string) ([]Hit, error) {
	if law.Matcher.Kind == KindGoBenchCeiling {
		return goBenchCeilingHits(view, law, requireData)
	}
	return jsonCeilingHits(view, law, requireData, targetDir)
}

// goBenchLineRE matches one `go test -bench -benchmem` result line: a name
// (optionally suffixed `-N` for GOMAXPROCS), an iteration count, then the
// metric columns (ns/op, B/op, allocs/op, ...) as free text this function
// scans separately.
var goBenchLineRE = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+(.*)$`)

// goBenchMetricRE pulls one `<value> <unit>` pair out of a bench line's
// metric columns.
var goBenchMetricRE = regexp.MustCompile(`(-?\d+)\s+(\S+)`)

// goBenchEnforcedUnits is what this law judges. sec/op (and legacy ns/op) is
// wall-clock noise — informational, never a ceiling; B/op and allocs/op are
// close to deterministic for the same code, so a rise in either is real.
var goBenchEnforcedUnits = map[string]bool{"B/op": true, "allocs/op": true}

// goBenchCeilingHits reads B/op and allocs/op out of a Go benchmark text
// file and reports one hit per (file, benchmark name, metric) — the WORST
// (largest) value seen for that triple, since a `-count` re-record carries
// several repeated lines for the same name and a ceiling must not let the
// best of several runs hide a regression the others show.
func goBenchCeilingHits(view treeView, law Law, requireData bool) ([]Hit, error) {
	files, err := viewGlobFiles(view, law.Matcher.Files)
	if err != nil {
		return nil, fmt.Errorf("law %q: %w", law.Name, err)
	}
	if len(files) == 0 && requireData {
		return nil, fmt.Errorf(
			"law %q: no file matched %q — armed with nothing to read, a clean verdict would be over data that does not exist",
			law.Name, law.Matcher.Files)
	}
	worst := map[string]int{}
	var order []string
	for _, rel := range files {
		data, err := view.read(rel)
		if err != nil {
			if vanished(err) {
				continue // a file that vanished mid-walk is not a finding
			}
			return nil, fmt.Errorf("law %q: %w", law.Name, &ScanReadError{Path: rel, Err: err})
		}
		for _, line := range strings.Split(string(data), "\n") {
			m := goBenchLineRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
			if m == nil {
				continue
			}
			name := m[1]
			for _, metric := range goBenchMetricRE.FindAllStringSubmatch(m[2], -1) {
				unit := metric[2]
				if !goBenchEnforcedUnits[unit] {
					continue
				}
				v, err := strconv.Atoi(metric[1])
				if err != nil {
					continue // not our concern here; the value simply cannot ceiling
				}
				key := rel + "|" + name + "|" + unit
				if _, seen := worst[key]; !seen {
					order = append(order, key)
				}
				if v > worst[key] {
					worst[key] = v
				}
			}
		}
	}
	sort.Strings(order)
	hits := make([]Hit, 0, len(order))
	for _, key := range order {
		hits = append(hits, Hit{
			Law: law.Name, File: key, Weight: worst[key],
			Key:  key,
			What: fmt.Sprintf("%s = %d", key, worst[key]),
		})
	}
	return hits, nil
}
