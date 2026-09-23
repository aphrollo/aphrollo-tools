package tdd

import (
	"path/filepath"
	"regexp"
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
