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
	// Denies counts every decision that REFUSED something or waived a rule,
	// keyed by the full verdict ("pretooluse-denied:test-sleep",
	// "override-off", "smell-escape:disabled-test"). A hatch nobody counts is
	// a hatch nobody manages. bound: one entry per policy name in the log.
	Denies map[string]int
	// Receipts counts the mutation-receipt stage's outcome side by side:
	// "accepted", "carried", "rejected". Each carries a dynamic suffix in the
	// log itself (a tree hash, counts, a from->to pair), so this collapses
	// them by PREFIX rather than by the full verdict the way Denies does —
	// otherwise every accepted receipt would be its own one-off row, and an
	// accepted receipt used to leave no count at all (issue #136).
	Receipts map[string]int
	// StandDowns counts every verdict that decided not to block and told
	// nobody but stderr about it — a skip, a fail-open, or an unverifiable
	// result — keyed by the verdict itself. Before this, "queued-skipped" was
	// the only stand-down with a row anywhere; "skipped", "runner-missing",
	// "lint-skipped", "receipt-unverifiable" and "receipt-unpinned" reached
	// gate.log and stopped being counted at all (issue #320). bound: one
	// entry per stand-down verdict the log contains.
	StandDowns map[string]int
	// LockWaitMax is the longest build-slot wait seen, in seconds. It is kept
	// out of Median/Max on purpose: a queued gate run is a busy box, not a
	// slow suite, and folding the two made contention look like a regression.
	LockWaitMax float64
	Median      float64
	Max         float64
	Lines       int
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
	"green", "red", "blocked", "timeout", "timeout-rejected", "vacuous-rejected",
	"queued-skipped", "queued-rejected", "deferred", lockWaitVerdict,
}

// lockWaitVerdict is the entry a stage writes when it spent long enough
// queued for a build slot to explain the run's wall time.
const lockWaitVerdict = "lock-wait"

// GateStats parses a gate.log stream, counting only entries at or after
// since (a zero time counts the whole log). A line it cannot parse is
// skipped rather than guessed at: the log is append-only text written by
// several processes, and a torn write must not distort a tally.
func GateStats(r io.Reader, since time.Time) Stats {
	s := Stats{
		ByStage:    map[string]map[string]int{},
		Timeouts:   map[string]int{},
		Deferred:   map[string]int{},
		Denies:     map[string]int{},
		Receipts:   map[string]int{},
		StandDowns: map[string]int{},
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
		if isDenyVerdict(e.verdict) {
			s.Denies[e.verdict]++
		}
		if isStandDownVerdict(e.verdict) {
			s.StandDowns[e.verdict]++
		}
		if outcome, ok := receiptOutcome(e.verdict); ok {
			s.Receipts[outcome]++
		}
		if e.verdict == lockWaitVerdict {
			if e.secs > s.LockWaitMax {
				s.LockWaitMax = e.secs
			}
			continue
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

// denyVerdictPrefixes are the verdicts that record a REFUSAL or a waiver
// rather than a run, and are therefore tallied by policy instead of by crate.
//
// receipt-forged and receipt-unsigned are here because they are exactly that:
// a receipt nothing signed, or one signed by something that is not the runner,
// is a rule being waived. Both reached gate.log and neither was ever counted,
// so the one number that would have shown a session hand-writing a receipt was
// invisible. queue-bypass is the same shape: the bypass is tolerated, and what
// makes it tolerable is that every use is counted.
//
// receipt-rejected: (trailing colon, unlike its unsuffixed neighbours here)
// is every blockReceipt/blockMissingReceipt cause, each carrying its own
// reason as a suffix — base-mismatch, worktree-dirty, unaccepted-survivor,
// missing, and so on. Before this they shared one undifferentiated
// receipt-rejected counter, so a 68% rejection rate over two days could not
// say which of eleven opposite-fix causes made it up (issue #376); this is
// what gives each cause its own row in the table below, the same way
// receipt-unsigned already got one.
var denyVerdictPrefixes = []string{
	"pretooluse-denied:", "commitmsg-rejected:", "override-", "smell-escape:",
	"receipt-forged", "receipt-unsigned", "receipt-rejected:", "queue-bypass",
	"mutants-worktree-failed", "git-discard-refused:", "standdown-",
}

func isDenyVerdict(verdict string) bool {
	for _, p := range denyVerdictPrefixes {
		if strings.HasPrefix(verdict, p) {
			return true
		}
	}
	return false
}

// standDownVerdictSuffixes is issue #320's own vocabulary: every verdict
// ending in one of these names a stage that decided not to block and is
// therefore counted, not just printed or logged uncounted. "-skipped" is a
// suffix ("lint-skipped", "docs-skipped", "no-runner-skipped"); the other
// three are Contains rather than HasSuffix because the logged verdict can
// carry trailing detail (failFirstStage's default verdict is literally
// "inconclusive (fail-open)", parenthesis and all).
var standDownVerdictSuffixes = []string{"unverifiable", "unpinned", "fail-open"} // standdown-logged: vocabulary constant, not a call site that itself stands down

// isStandDownVerdict reports whether verdict is a stand-down: "skipped" and
// "runner-missing" are bare exact matches (verdictFor's own two deliberate,
// non-blocking outcomes), everything else is judged by
// standDownVerdictSuffixes. "queued-skipped" also matches (HasSuffix
// "-skipped"), so it is counted here AND kept in its own contention line —
// the two answer different questions ("is the box busy" vs "is every
// stand-down counted somewhere") and are not mutually exclusive.
func isStandDownVerdict(verdict string) bool {
	switch verdict {
	case "skipped", "runner-missing":
		return true
	}
	if strings.HasSuffix(verdict, "-skipped") {
		return true
	}
	for _, s := range standDownVerdictSuffixes {
		if strings.Contains(verdict, s) {
			return true
		}
	}
	return false
}

// receiptOutcomePrefixes maps a gate.log verdict PREFIX to the mutation-
// receipt outcome it counts under. receipt-rejected is checked last: it is
// also a prefix of nothing else here, but the order keeps the intent
// explicit — the dynamic-suffix ones are matched first.
var receiptOutcomePrefixes = []struct{ prefix, outcome string }{
	{"receipt-accepted:", "accepted"},
	{"receipt-carried:", "carried"},
	{"receipt-rejected", "rejected"},
}

// receiptOutcome classifies a verdict as one of the receipt stage's three
// outcomes, "" and false for anything else.
func receiptOutcome(verdict string) (string, bool) {
	for _, p := range receiptOutcomePrefixes {
		if strings.HasPrefix(verdict, p.prefix) {
			return p.outcome, true
		}
	}
	return "", false
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

// stageMeasured reports whether stage has at least one entry counted under
// one of the tracked outcome columns (statsOutcomes) in this reading. A
// stage that only ever wrote OTHER verdicts — commitmsg-rejected:...,
// mutants-started:..., pretooluse-denied:... — is real activity (it lands in
// Denies or Receipts, and s.ByStage[stage] is non-nil), but this table's
// fixed vocabulary never applies to it, so every cell in its row is a
// confirmed-zero that never actually got measured. Rendering that row as
// nine zeros reads as "ran clean"; issue #369 is that it means "this table
// cannot see this stage" instead. A stage that DOES post at least one
// tracked outcome is measured, and every cell in its row — including a
// genuine zero among them — is a real count.
func stageMeasured(s Stats, stage string) bool {
	for _, o := range statsOutcomes {
		if s.Count(stage, o) != 0 {
			return true
		}
	}
	return false
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
		measured := stageMeasured(s, stage)
		for _, o := range statsOutcomes {
			if !measured {
				fmt.Fprintf(&b, "%17s", "-")
				continue
			}
			fmt.Fprintf(&b, "%17d", s.Count(stage, o))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%d entries; gate seconds: median %s, max %s\n",
		s.Lines, formatFloat(s.Median), formatFloat(s.Max))
	b.WriteString(contentionLine(s))
	writeCounts(&b, "timeouts by crate", s.Timeouts)
	writeCounts(&b, "deferred by crate", s.Deferred)
	writeCounts(&b, "denies / overrides", s.Denies)
	writeCounts(&b, "stand-downs", s.StandDowns)
	writeCounts(&b, "mutation receipts", s.Receipts)
	b.WriteString(escapeDebtLine())
	return b.String()
}

// contentionLine is the one line that says whether the box was busy: the
// longest a run sat waiting for a build slot, how many runs outlived their
// hook budget, and how many never started at all. It prints its zeros — a
// line that appears only under contention cannot be used to say a day was
// quiet, and finding these numbers by hand is the tally this exists to
// replace.
func contentionLine(s Stats) string {
	deferred := 0
	for _, n := range s.Deferred {
		deferred += n
	}
	queued := 0
	for _, byOutcome := range s.ByStage {
		queued += byOutcome["queued-skipped"]
	}
	return fmt.Sprintf("contention: longest build-slot wait %ss, %d deferred, %d queued-skipped\n",
		formatFloat(s.LockWaitMax), deferred, queued)
}

// writeCounts renders one tally line, biggest first, under its own label —
// the label is the whole caption, because the crate tallies and the policy
// tally are keyed by different things.
func writeCounts(b *strings.Builder, label string, counts map[string]int) {
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
	fmt.Fprintf(b, "%s:", label)
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
