package tdd

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Stats is one reading of pipeline health from gate.log: how often each stage
// reached each outcome, which crates time out or defer, and how long gate
// runs take. It answers "is the pipeline healthy" with numbers instead of an
// impression formed from whichever lines happened to scroll past.
type Stats struct {
	// ByStage[stage][outcome] is the tally. bound: one entry per stage and
	// outcome NAME the log contains (a fixed, tiny vocabulary).
	ByStage map[string]map[string]int
	// Timeouts / Deferred are per-crate counts, keyed on the last element of
	// the run's root — the crate is what an operator can act on.
	Timeouts map[string]int
	Deferred map[string]int
	Median   float64
	Max      float64
	Lines    int
}

// Count is the tally for one stage/outcome pair, zero when it never happened.
func (s Stats) Count(stage, outcome string) int {
	return s.ByStage[stage][outcome]
}

// statsStages is the stage vocabulary the table always shows, so "zero" and
// "never ran" are not the same blank.
var statsStages = []string{"postedit", "precommit", "premergecommit"}

// statsOutcomes is the outcome vocabulary, in the order a reader cares about.
var statsOutcomes = []string{
	"green", "red", "blocked", "timeout", "timeout-rejected",
	"queued-skipped", "queued-rejected", "deferred",
}

// GateStats parses a gate.log stream, counting only entries at or after
// since (a zero time counts the whole log). A line it cannot parse is
// skipped rather than guessed at: the log is append-only text written by
// several processes, and a torn write must not distort a tally.
func GateStats(r io.Reader, since time.Time) Stats {
	s := Stats{
		ByStage:  map[string]map[string]int{},
		Timeouts: map[string]int{},
		Deferred: map[string]int{},
	}
	var secs []float64
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || (!since.IsZero() && e.at.Before(since)) {
			continue
		}
		s.Lines++
		if s.ByStage[e.stage] == nil {
			s.ByStage[e.stage] = map[string]int{}
		}
		s.ByStage[e.stage][e.verdict]++
		crate := logRootCrate(e.root)
		switch {
		case strings.HasPrefix(e.verdict, "timeout"):
			s.Timeouts[crate]++
		case strings.HasPrefix(e.verdict, "deferred"):
			s.Deferred[crate]++
		}
		secs = append(secs, e.secs)
	}
	sort.Float64s(secs)
	if n := len(secs); n > 0 {
		s.Max = secs[n-1]
		s.Median = secs[n/2]
	}
	return s
}

// logRootCrate names the crate a log entry's root belongs to: the root's
// last path element, split on BOTH separators. gate.log is written on one
// box and read on another, so a Windows root reaches a Linux reader whose
// filepath.Base treats the backslashes as ordinary name characters.
func logRootCrate(root string) string {
	root = strings.TrimRight(root, `\/`)
	if i := strings.LastIndexAny(root, `\/`); i >= 0 {
		return root[i+1:]
	}
	return root
}

// gateEntry is one parsed log line.
type gateEntry struct {
	at      time.Time
	stage   string
	root    string
	verdict string
	secs    float64
}

// parseGateLine reads "<ts> <stage> <root> <cmd...> <verdict> <secs>s". The
// COMMAND contains spaces, so the line is read from both ends inward.
func parseGateLine(line string) (gateEntry, bool) {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 5 {
		return gateEntry{}, false
	}
	at, err := time.Parse(time.RFC3339, f[0])
	if err != nil {
		return gateEntry{}, false
	}
	secs, err := strconv.ParseFloat(strings.TrimSuffix(f[len(f)-1], "s"), 64)
	if err != nil {
		return gateEntry{}, false
	}
	return gateEntry{at: at, stage: f[1], root: f[2], verdict: f[len(f)-2], secs: secs}, true
}

// RenderGateStats draws the one table the command prints.
func RenderGateStats(s Stats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s", "stage")
	for _, o := range statsOutcomes {
		fmt.Fprintf(&b, "%17s", o)
	}
	b.WriteString("\n")
	stages := append([]string{}, statsStages...)
	for stage := range s.ByStage {
		if !contains(stages, stage) {
			stages = append(stages, stage)
		}
	}
	sort.Strings(stages)
	for _, stage := range stages {
		fmt.Fprintf(&b, "%-16s", stage)
		for _, o := range statsOutcomes {
			fmt.Fprintf(&b, "%17d", s.Count(stage, o))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%d entries; gate seconds: median %s, max %s\n",
		s.Lines, formatFloat(s.Median), formatFloat(s.Max))
	writeCrateCounts(&b, "timeouts", s.Timeouts)
	writeCrateCounts(&b, "deferred", s.Deferred)
	return b.String()
}

func writeCrateCounts(b *strings.Builder, label string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	fmt.Fprintf(b, "%s by crate:", label)
	for _, n := range names {
		fmt.Fprintf(b, " %s=%d", n, counts[n])
	}
	b.WriteString("\n")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// formatFloat renders a seconds figure without trailing zeros.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// GateLogPath is where the gate writes its log, "" when there is no state
// dir. Exported so the stats command can read it.
func GateLogPath() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "gate.log")
}
