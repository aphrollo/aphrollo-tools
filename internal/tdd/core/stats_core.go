package core

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GateLogPath is where the gate writes its log, "" when there is no state
// dir. Exported so the stats command can read it.
func GateLogPath() string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "gate.log")
}

// gateEntry is one parsed log line.
type gateEntry struct {
	At    time.Time
	Stage string
	Root  string
	// Cmd is the invocation that produced the verdict, "" for a stage that
	// logged none. It is what the scope law reads (runscope.go): the WIDTH
	// of a recorded run is derivable from the command already on disk, so
	// judging whether a verdict may refuse a rerun needs no new log field.
	Cmd     string
	Verdict string
	Secs    float64
	// File is the file or session a deny, an override or a session line is
	// about. The command of such a line is what someone typed and is not
	// recorded; this field is the one thing of it the escape reports read.
	File string
}

// parseGateLine reads "<ts> <stage> <root> <cmd...> <verdict> <secs>s". The
// COMMAND contains spaces, so the line is read from both ends inward. A
// verdict with no whitespace of its own is the LAST field before the
// duration, f[len(f)-2]; one quoteVerdict quoted because it carries
// whitespace (failFirstStage's "inconclusive (fail-open)") is recovered
// whole via quotedVerdict instead, since strings.Fields alone would split it
// and silently keep only its last word.
func parseGateLine(line string) (gateEntry, bool) {
	line = strings.TrimSpace(line)
	f := strings.Fields(line)
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
	verdict := f[len(f)-2]
	cmd := strings.Join(f[3:len(f)-2], " ")
	if m := quotedVerdict.FindStringSubmatchIndex(line); m != nil {
		if uq, err := strconv.Unquote(line[m[2]-1 : m[3]+1]); err == nil {
			verdict = uq
		}
		// A quoted verdict occupies more than one field, so the command is
		// no longer f[3:len(f)-2] — it is everything between the root and
		// the opening quote.
		if head := strings.Fields(strings.TrimSpace(line[:m[2]-1])); len(head) > 3 {
			cmd = strings.Join(head[3:], " ")
		} else {
			cmd = ""
		}
	}
	return gateEntry{At: at, Stage: f[1], Root: f[2], Cmd: cmd, Verdict: verdict, Secs: secs}, true
}

// quotedVerdict finds a verdict quoteVerdict wrote as a Go string literal:
// greedy `.*` before the literal lands on the LAST quoted span in the line,
// which is exactly where quoteVerdict puts it (immediately before the
// trailing duration field) even if an earlier field — the command — carries
// quotes of its own (issue #467).
var quotedVerdict = regexp.MustCompile(`^\S+ \S+ \S+ .* "((?:[^"\\]|\\.)*)" \S+$`)

// gateEntryOf is the entry a gate stage line's event stands for; ok is false
// for every other event. A stage line is the event AppendGateLogDetail writes:
// it names a stage. The prepush "push" event names one too but is no stage line.
// An event written before the root was kept falls back to its repo.
func gateEntryOf(e Event) (gateEntry, bool) {
	if e.Stage == "" || e.Kind == "push" {
		return gateEntry{}, false
	}
	at, err := time.Parse(time.RFC3339, e.At)
	if err != nil {
		return gateEntry{}, false // absence-ok: an event whose time does not parse is no stage line
	}
	root := e.Root
	if root == "" {
		root = e.Repo
	}
	return gateEntry{At: at, Stage: e.Stage, Root: root, Cmd: e.Cmd, Verdict: e.Verdict, Secs: e.Secs, File: e.Detail["file"]}, true
}

// entriesOf maps events to entries, keeping those at or after since.
func entriesOf(events []Event, since time.Time) []gateEntry {
	var out []gateEntry
	for _, ev := range events {
		if e, ok := gateEntryOf(ev); ok && !e.At.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// readGateEntries is the stage lines of the repository root belongs to since
// since (the zero time: all), oldest first. They come from the repo's event
// log: that, not gate.log, is where the gate records what every stage did.
func readGateEntries(root string, since time.Time) []gateEntry {
	return entriesOf(readEventsSince(root, since), since)
}

// readAllGateEntries is readGateEntries over every repository the box keeps a
// log for, oldest first: the older single-file log and each directory under
// <state root>/state.
func readAllGateEntries(since time.Time) []gateEntry {
	var events []Event
	if dir := StateDir(); dir != "" {
		events = append(events, readEventFileIfRecent(filepath.Join(dir, "events.jsonl"), since)...)
	}
	if root := StateRoot(); root != "" {
		dirs, _ := filepath.Glob(filepath.Join(root, "state", "*"))
		for _, d := range dirs {
			events = append(events, readEventDirSince(d, since)...)
		}
	}
	entries := entriesOf(events, since)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })
	return entries
}

// formatGateLine renders an entry as the line gate.log kept:
// "<RFC3339> <stage> <root> <cmd...> <verdict> <secs>s". The text parsers of the
// escape reports still read that shape, so they are fed lines rendered from
// events rather than a second log. A line with no command of its own carries its
// File in that place, where the reports look for it.
func formatGateLine(e gateEntry) string {
	cmd := e.Cmd
	if cmd == "" {
		cmd = e.File
	}
	return fmt.Sprintf("%s %s %s %s %s %.1fs\n",
		e.At.UTC().Format(time.RFC3339), e.Stage, LogToken(e.Root), cmd, quoteVerdict(e.Verdict), e.Secs)
}

// gateEntryLines renders entries, oldest first, as gate.log lines.
func gateEntryLines(entries []gateEntry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(formatGateLine(e))
	}
	return b.String()
}

// gateLinesSince is every repo's stage lines since since (the zero time: all),
// rendered as gate.log lines and oldest first, for the text parsers of the
// escape reports (stats, the demotion trend, the override scan).
func gateLinesSince(since time.Time) string { return gateEntryLines(readAllGateEntries(since)) }
