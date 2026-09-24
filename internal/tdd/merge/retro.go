package merge

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The post-merge retro. After `workspace merge` lands a PR, one bounded gh
// batch reads the PR's journey and the gate log and the lane's reflog read
// the local side of it. Each piece of friction becomes a fact with numbers,
// each fact class asks one pointed question naming the one place its answer
// goes, and a merge with no friction leaves no trace at all. The retro is
// recorded for the session that ran the merge and printed by that session's
// next hook (retro_store.go), never by the merge itself.

// retroClosing is the retro's last line.
const retroClosing = "a retro answered only in prose is not answered"

// retroFact is one line of friction and the classes it belongs to.
type retroFact struct {
	classes []string
	text    string
}

// retroLane is the local side of the journey.
type retroLane struct {
	gate      []gateEntry
	conflicts int
}

// PostMergeRetro is the tail of a landed merge: collect, judge against the
// repo's rules, record for delivery. It never blocks and never fails the
// merge; a gh failure is one "retro skipped" line on stderr.
func PostMergeRetro(mainRepo, worktree, branch string, pr int, stderr io.Writer) {
	cfg := loadRetroConfig(worktree)
	if !cfg.active() {
		return
	}
	c := retroCollector{dir: worktree, deadline: retroNow().Add(retroBudget)}
	info, runs, err := c.collect(pr)
	if err != nil {
		fmt.Fprintf(stderr, "retro skipped: %v\n", err)
		AppendGateLog("retro", worktree, "retro #"+strconv.Itoa(pr), "retro-skipped", 0)
		return
	}
	lane := readRetroLane(worktree, branch)
	text := renderRetro(info, retroFacts(info, runs, lane, cfg), cfg)
	if text == "" {
		return
	}
	if err := recordRetro(SessionID(), mainRepo, pr, text); err != nil {
		fmt.Fprintf(stderr, "retro not recorded (%v):\n%s", err, text)
	}
}

// readRetroLane reads the lane's gate log entries since its branch was
// created, and the conflicts its reflog shows were resolved by hand.
func readRetroLane(worktree, branch string) retroLane {
	var lane retroLane
	since := time.Time{}
	if out, err := git(worktree, "reflog", "show", "--date=unix", "--format=%gd%x09%gs", "refs/heads/"+branch); err == nil {
		for line := range strings.Lines(out) {
			selector, subject, _ := strings.Cut(strings.TrimSpace(line), "\t")
			if strings.HasPrefix(subject, "commit (merge): ") {
				lane.conflicts++
			}
			if at, ok := reflogTime(selector); ok {
				since = at // the last line is the branch's creation
			}
		}
	}
	until := retroNow()
	f, err := os.Open(GateLogPath())
	if err != nil {
		return lane
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || !sameProject(e.Root, worktree) || e.At.Before(since) || e.At.After(until) {
			continue
		}
		lane.gate = append(lane.gate, e)
	}
	return lane
}

// reflogTime reads the unix time out of a "<ref>@{<unix>}" selector.
func reflogTime(selector string) (time.Time, bool) {
	_, rest, ok := strings.Cut(selector, "@{")
	if !ok {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(rest, "}"), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}

// retroFacts turns the journey into facts, in a fixed order: CI reds, flaky
// re-runs, pushes, conflicts, refusals, the merge's own duration.
func retroFacts(info retroPR, runs []retroRun, lane retroLane, cfg retroConfig) []retroFact {
	var facts []retroFact
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	shas := map[string]bool{}
	for _, r := range runs {
		shas[r.HeadSha] = true
		for _, j := range r.Failed {
			facts = append(facts, ciRedFact(r, j, lane))
		}
		if r.Attempt > 1 && r.Conclusion == "success" {
			facts = append(facts, retroFact{[]string{classFlakyRerun},
				fmt.Sprintf("%s passed on attempt %d after a failed attempt", r.Workflow, r.Attempt)})
		}
	}
	if n := len(shas) - 1; n > 0 {
		facts = append(facts, retroFact{[]string{classExtraPush}, retroPlural(n, "push", "pushes") + " after open"})
	}
	if lane.conflicts > 0 {
		facts = append(facts, retroFact{[]string{classMergeConflict},
			retroPlural(lane.conflicts, "merge conflict", "merge conflicts") + " resolved in the lane"})
	}
	if f, ok := refusalFact(lane); ok {
		facts = append(facts, f)
	}
	bar := time.Duration(cfg.SlowMinutes) * time.Minute
	if open := info.MergedAt.Sub(info.CreatedAt); cfg.SlowMinutes > 0 && open > bar {
		facts = append(facts, retroFact{[]string{classSlowMerge},
			fmt.Sprintf("%s open→merge, over the %s bar", retroDuration(open), retroDuration(bar))})
	}
	return facts
}

// ciRedFact is one failed job, with a mutation job's counts and whether a
// local green preceded the run.
func ciRedFact(r retroRun, j retroJob, lane retroLane) retroFact {
	var classes, detail []string
	if m := j.Mutants; m != nil {
		if m.Survivors > 0 {
			classes = append(classes, classMutantSurvivor)
			detail = append(detail, retroPlural(m.Survivors, "survivor", "survivors"))
		}
		if m.TimedOut > 0 {
			detail = append(detail, fmt.Sprintf("%d timed out", m.TimedOut))
		}
		if m.Unmeasured > 0 {
			detail = append(detail, fmt.Sprintf("%d unmeasured", m.Unmeasured))
		}
		if m.TimedOut+m.Unmeasured > 0 {
			classes = append(classes, classMutantTimeout)
		}
	}
	text := "CI " + j.Name + " red"
	if len(detail) > 0 {
		text += " (" + strings.Join(detail, ", ") + ")"
	}
	if localGreenBefore(lane, r.CreatedAt) {
		return retroFact{append([]string{classCIRedAfterGreen}, classes...), text + " after local green"}
	}
	return retroFact{append([]string{classCIRed}, classes...), text + " with no local green before it"}
}

// localGreenBefore reports whether the lane logged a green at or before at.
func localGreenBefore(lane retroLane, at time.Time) bool {
	for _, e := range lane.gate {
		if e.Verdict == "green" && !e.At.After(at) {
			return true
		}
	}
	return false
}

// refusalFact counts the commit and merge gates' refusals in the lane.
func refusalFact(lane retroLane) (retroFact, bool) {
	counts := map[string]int{}
	total := 0
	for _, e := range lane.gate {
		if isGateRefusal(e.Stage, e.Verdict) {
			counts[e.Verdict]++
			total++
		}
	}
	if total == 0 {
		return retroFact{}, false
	}
	var parts []string
	for v, n := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", v, n))
	}
	sort.Strings(parts)
	return retroFact{[]string{classGateRefusal},
		retroPlural(total, "gate refusal", "gate refusals") + " in the lane (" + strings.Join(parts, ", ") + ")"}, true
}

// isGateRefusal reports whether a gate.log entry is the commit or merge gate
// refusing: an edit hook's red is the loop working, not a refusal.
func isGateRefusal(stage, verdict string) bool {
	switch stage {
	case "precommit", "premergecommit", "premerge", "commitmsg", "prepush":
	default:
		return false
	}
	if verdict == "blocked" || verdict == "violated" || strings.HasPrefix(verdict, "commitmsg-rejected:") ||
		strings.HasPrefix(verdict, "mutants-refused:") {
		return true
	}
	for _, suffix := range []string{"-rejected", "-blocked", "-refused"} {
		if strings.HasSuffix(verdict, suffix) {
			return true
		}
	}
	return false
}

// renderRetro renders the facts the repo's rules let through: a header, one
// line per fact, one question per class, the closing line. "" when nothing
// fired.
func renderRetro(info retroPR, facts []retroFact, cfg retroConfig) string {
	var lines, questions []string
	asked := map[string]bool{}
	for _, f := range facts {
		fired := false
		for _, class := range f.classes {
			if !cfg.On[class] {
				continue
			}
			fired = true
			if s, ok := cfg.Sinks[class]; ok && !asked[class] {
				asked[class] = true
				questions = append(questions, "? "+s.Question+" → "+s.Sink)
			}
		}
		if fired {
			lines = append(lines, fmt.Sprintf("#%d: %s", info.Number, f.text))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	head := fmt.Sprintf("retro #%d %s (%s open→merge):", info.Number, info.HeadRefName,
		retroDuration(info.MergedAt.Sub(info.CreatedAt)))
	all := append(append(append([]string{head}, lines...), questions...), retroClosing)
	return strings.Join(all, "\n") + "\n"
}

// retroDuration renders whole minutes: "43m", "2h10m".
func retroDuration(d time.Duration) string {
	m := int(d / time.Minute)
	if m >= 60 {
		return fmt.Sprintf("%dh%02dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// retroPlural renders "1 push", "2 pushes".
func retroPlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
