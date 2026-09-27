package merge

import (
	"fmt"
	"strconv"
	"strings"
)

// The post-merge retro's trigger rules and sinks are data, read from the same
// two tables every other repo-declared gate key lives in (mutantsConfigTables):
//
//	retro-on = ["ci-red-after-local-green", "extra-push", ...]
//	retro-slow-merge-minutes = 120
//	retro-sinks = ["<class> -> <question> -> <sink>", ...]
//
// An absent retro-on means every class below; `retro-on = []` turns them all
// off. slow-merge is not listed there: it is on whenever the minutes are
// above zero, and `retro-slow-merge-minutes = 0` turns it off. retro-sinks
// overrides the built-in question and sink per class and leaves every class
// it does not name on its default. A row that does not split into three
// non-empty fields is skipped and named in a warning (Warnings), printed when
// the retro loads its config and by `aphrollo check`.
const (
	retroOnKey    = "retro-on"
	retroSlowKey  = "retro-slow-merge-minutes"
	retroSinksKey = "retro-sinks"
)

// The fact classes a retro can report.
const (
	classCIRedAfterGreen = "ci-red-after-local-green"
	classCIRed           = "ci-red"
	classMutantSurvivor  = "mutant-survivor"
	classMutantTimeout   = "mutant-timeout"
	classFlakyRerun      = "flaky-rerun"
	classExtraPush       = "extra-push"
	classMergeConflict   = "merge-conflict"
	classGateRefusal     = "gate-refusal"
	classSlowMerge       = "slow-merge"
)

// retroDefaultOn is retro-on when a repo declares none.
var retroDefaultOn = []string{
	classCIRedAfterGreen, classCIRed, classMutantSurvivor, classMutantTimeout,
	classFlakyRerun, classExtraPush, classMergeConflict, classGateRefusal,
}

// retroDefaultSlowMinutes is retro-slow-merge-minutes when a repo declares none.
const retroDefaultSlowMinutes = 120

// retroSink is one class's pointed question and the one place its answer is
// recorded.
type retroSink struct {
	Question string
	Sink     string
}

const (
	sinkEscape = `aphrollo gate escape record "<reason>"`
	sinkIssue  = `aphrollo issue "<title>" --label <theme>`
	sinkNote   = "a memory/feedback note or an issue"
)

// retroDefaultSinks maps every class to its sanctioned sink: a red a local
// stage could have caught is an escape, a defect in the pipeline itself is
// an issue, and a habit is a feedback note.
var retroDefaultSinks = map[string]retroSink{
	classCIRedAfterGreen: {"which local stage or law would have caught this?", sinkEscape},
	classCIRed:           {"why did this reach CI with no local green before it?", sinkEscape},
	classMutantSurvivor:  {"which test kills each survivor, and why did the local measurement not refuse it first?", sinkEscape},
	classMutantTimeout:   {"which test or budget let mutants time out unmeasured?", sinkIssue},
	classFlakyRerun:      {"which test flaked, and what makes it deterministic?", sinkIssue},
	classExtraPush:       {"what rule avoids the extra push?", sinkNote},
	classMergeConflict:   {"which overlap with another lane caused it, and what lane boundary avoids it?", sinkNote},
	classGateRefusal:     {"which refusal could the change have avoided, and what rule says how?", sinkNote},
	classSlowMerge:       {"what held the PR open?", sinkIssue},
}

// retroConfig is one repo's reading of the three keys.
type retroConfig struct {
	On          map[string]bool
	SlowMinutes int
	Sinks       map[string]retroSink
	Warnings    []string
}

// active reports whether any class can fire, so a repo that turned them all
// off pays for no gh call.
func (c retroConfig) active() bool {
	return len(c.On) > 0
}

// loadRetroConfig reads root's retro keys, the first table declaring a key
// winning for that key.
func loadRetroConfig(root string) retroConfig {
	c := retroConfig{On: map[string]bool{}, SlowMinutes: retroDefaultSlowMinutes, Sinks: map[string]retroSink{}}
	on, onSet := retroDefaultOn, false
	slowSet := false
	var sinks []string
	sinksPath := ""
	for _, t := range mutantsConfigTables(root) {
		if _, set := tomlStringIn(t.Path, t.Table, retroOnKey); set && !onSet {
			on, onSet = tomlStringsIn(t.Path, t.Table, retroOnKey), true
		}
		if raw, set := tomlStringIn(t.Path, t.Table, retroSlowKey); set && !slowSet {
			if n, err := strconv.Atoi(raw); err == nil {
				c.SlowMinutes, slowSet = n, true
			}
		}
		if sinks == nil {
			sinks, sinksPath = tomlStringsIn(t.Path, t.Table, retroSinksKey), t.Path
		}
	}
	for _, class := range on {
		c.On[class] = true
	}
	if c.SlowMinutes > 0 {
		c.On[classSlowMerge] = true
	}
	for class, s := range retroDefaultSinks {
		c.Sinks[class] = s
	}
	for _, entry := range sinks {
		class, sink, ok := parseRetroSink(entry)
		if !ok {
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"%s: %s row %q is not \"<class> -> <question> -> <sink>\"; skipped, the class keeps its default",
				sinksPath, retroSinksKey, entry))
			continue
		}
		c.Sinks[class] = sink
	}
	return c
}

// parseRetroSink splits one retro-sinks row into its class and sink; ok is
// false unless the row has exactly three fields and none of them is blank.
func parseRetroSink(entry string) (string, retroSink, bool) {
	parts := strings.Split(entry, " -> ")
	if len(parts) != 3 {
		return "", retroSink{}, false
	}
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
		if parts[i] == "" {
			return "", retroSink{}, false
		}
	}
	return parts[0], retroSink{Question: parts[1], Sink: parts[2]}, true
}

// RetroConfigWarnings is one line per retro-sinks row in root's config that
// the retro skips, each naming the file and the row.
func RetroConfigWarnings(root string) []string {
	return loadRetroConfig(root).Warnings
}
