package escape

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// The gate writes a line for everything it does and nobody reads gate.log.
// So once a week — at the one moment nothing is waiting on a build — a
// session start gets ONE line: how often the last seven days went green, how
// often a run was skipped because the box was busy, how many refusals and
// waivers there were, and how much escape debt is open. Weekly rather than
// per-session because a health line on every start is a line nobody reads by
// the third one.

const weeklyStampFile = "weekly-digest-last-run"

const weeklyEvery = 7 * 24 * time.Hour

// maybeWeeklyDigest returns the digest line when a week has passed since the
// last one, and stamps it. "" otherwise, or when there is no log to read.
func maybeWeeklyDigest(now time.Time) string {
	path := gcStatePath(weeklyStampFile)
	if path == "" {
		return ""
	}
	if info, err := os.Stat(path); err == nil && now.Sub(info.ModTime()) < weeklyEvery {
		return ""
	}
	line := weeklyDigest(now)
	if line == "" {
		return ""
	}
	_ = os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)), 0o600)
	_ = os.Chtimes(path, now, now)
	return line
}

// weeklyDigest reads the last seven days of gate.log and renders the one
// line. "" when there is nothing to report at all.
func weeklyDigest(now time.Time) string {
	f, err := os.Open(GateLogPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	s := GateStats(f, now.Add(-weeklyEvery))

	runs, green, queued := 0, 0, 0
	for _, outcomes := range s.ByStage {
		for name, n := range outcomes {
			if name == lockWaitVerdict || isDenyVerdict(name) {
				continue
			}
			runs += n
			switch name {
			case string(Green):
				green += n
			case "queued-skipped":
				queued += n
			}
		}
	}
	denies, overrides := 0, 0
	for verdict, n := range s.Denies {
		if strings.HasPrefix(verdict, "override-") {
			overrides += n
			continue
		}
		denies += n
	}

	open, oldest := OpenEscapes()
	return fmt.Sprintf("aphrollo: last 7d — green %d%%, queued-skipped %d%%, denies %d, overrides %d, open escapes %d (oldest %dd)",
		percent(green, runs), percent(queued, runs), denies, overrides, open, int(oldest.Hours()/24))
}

// percent is the share of total, 0 when nothing ran — a rate over no runs is
// not 100%, it is unknown, and 0 is the honest placeholder beside a run count
// the reader can see is empty.
func percent(n, total int) int {
	if total == 0 {
		return 0
	}
	return n * 100 / total
}

// escapeDebtLine is what `gate stats` prints about the open escapes: the two
// numbers that decide whether the loop is being worked.
func escapeDebtLine() string {
	open, oldest := OpenEscapes()
	return fmt.Sprintf("open escapes: %d (oldest %dd)\n", open, int(oldest.Hours()/24))
}
