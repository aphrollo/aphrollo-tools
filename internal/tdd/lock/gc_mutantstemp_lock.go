package lock

import (
	"strconv"
	"strings"
)

// csvPids reads the pid column out of tasklist's CSV rows. A row for a
// filter that matched nothing carries no quoted pid, so it yields none.
func csvPids(out string) []int {
	var pids []int
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), `","`)
		if len(fields) < 2 {
			continue
		}
		if n, err := strconv.Atoi(strings.Trim(fields[1], `"`)); err == nil {
			pids = append(pids, n)
		}
	}
	return pids
}
