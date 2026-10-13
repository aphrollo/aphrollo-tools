package measure

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// An escape is attributed to the lane that caused it, which the on/off comparison needs:
// an off lane has no gate to record one. Three sources say so, each a row of
// attributionSources; all are read-time only and never rewrite the log. An escape two
// sources see counts once, for the first source of the table.

// EscapeIssue is an issue of the tracker as the attribution reads it.
type EscapeIssue struct {
	Number int
	Title  string
	Body   string
}

// RedRun is a red run on the trunk: a CI run or a merge-gate run that failed.
type RedRun struct {
	At  time.Time
	Ref string
}

// AttributionInput is everything the sources read. PRBranch maps a PR number to its head
// branch (the lane), asking the host; nil asks nothing.
type AttributionInput struct {
	Events   []tdd.Event
	Issues   []EscapeIssue
	PRBranch func(pr int) (branch string, ok bool)
	MainReds []RedRun
}

// Attribution is one escape and the lane it is blamed on. Key says which escape it is: the
// PR when one is known, so two sources naming one PR agree.
type Attribution struct {
	Lane   string
	Source string
	Key    string
}

// mergeFact is a merge the log records.
type mergeFact struct {
	at   time.Time
	lane string
	pr   string
	head string
}

// attrWorld is the input with its merges read once.
type attrWorld struct {
	in     AttributionInput
	merges []mergeFact // by time
}

// attributionSources is the table of sources, in the order that wins a tie.
var attributionSources = []struct {
	Name string
	Read func(*attrWorld) []Attribution
}{
	{"record", (*attrWorld).fromRecords},
	{"issue", (*attrWorld).fromIssues},
	{"main-red", (*attrWorld).fromMainReds},
}

// AttributeEscapes folds the three sources into the escapes of each lane.
func AttributeEscapes(in AttributionInput) []Attribution {
	w := &attrWorld{in: in}
	for _, e := range in.Events {
		if e.Kind != "merge" || e.Verdict != "ok" || e.Lane == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil {
			continue
		}
		w.merges = append(w.merges, mergeFact{at: at, lane: e.Lane, pr: e.Detail["pr"], head: e.Detail["head"]})
	}
	sort.SliceStable(w.merges, func(i, j int) bool { return w.merges[i].at.Before(w.merges[j].at) })
	seen := map[[2]string]bool{}
	var out []Attribution
	for _, src := range attributionSources {
		rows := src.Read(w)
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Lane != rows[j].Lane {
				return rows[i].Lane < rows[j].Lane
			}
			return rows[i].Key < rows[j].Key
		})
		for _, a := range rows {
			if k := [2]string{a.Lane, a.Key}; !seen[k] {
				seen[k] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// prKey is the key of an escape that names a PR.
func prKey(pr string) string { return "pr:" + pr }

// laneOfPR is the lane a PR merged from: the merge the log holds, else the PR's head
// branch from the host.
func (w *attrWorld) laneOfPR(pr string) (string, bool) {
	for _, m := range w.merges {
		if m.pr == pr {
			return m.lane, true
		}
	}
	if n, err := strconv.Atoi(pr); err == nil && w.in.PRBranch != nil {
		return w.in.PRBranch(n)
	}
	return "", false
}

// fromRecords: `gate escape record` writes an escape event for the lane it ran in, or for
// the lane of the PR it names when it ran from the trunk.
func (w *attrWorld) fromRecords() []Attribution {
	var out []Attribution
	for _, e := range w.in.Events {
		if e.Kind != "escape" || e.Verdict == "false-positive" {
			continue
		}
		pr := e.Detail["pr"]
		key := "record:" + e.Detail["id"]
		if e.Detail["id"] == "" {
			key = "record:" + e.At
		}
		if pr != "" {
			key = prKey(pr)
		}
		lane := e.Lane
		if lane == "" || abTrunk(lane) {
			l, ok := w.laneOfPR(pr)
			if pr == "" || !ok {
				continue
			}
			lane = l
		}
		out = append(out, Attribution{Lane: lane, Source: "record", Key: key})
	}
	return out
}

var (
	prRef  = regexp.MustCompile(`(?i)\bPR\s*#?(\d+)|/pull/(\d+)|\(#(\d+)\)`)
	shaRef = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// fromIssues: an issue titled `escape:` names the PR or the merge sha that caused it.
func (w *attrWorld) fromIssues() []Attribution {
	var out []Attribution
	for _, is := range w.in.Issues {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(is.Title)), "escape:") {
			continue
		}
		text := is.Title + "\n" + is.Body
		if m := prRef.FindStringSubmatch(text); m != nil {
			pr := m[1] + m[2] + m[3]
			if lane, ok := w.laneOfPR(pr); ok {
				out = append(out, Attribution{Lane: lane, Source: "issue", Key: prKey(pr)})
			}
			continue
		}
		for _, sha := range shaRef.FindAllString(text, -1) {
			if m, ok := w.mergeOfSHA(sha); ok {
				key := "sha:" + m.head
				if m.pr != "" {
					key = prKey(m.pr)
				}
				out = append(out, Attribution{Lane: m.lane, Source: "issue", Key: key})
				break
			}
		}
	}
	return out
}

func (w *attrWorld) mergeOfSHA(sha string) (mergeFact, bool) {
	for _, m := range w.merges {
		if m.head != "" && strings.HasPrefix(m.head, sha) {
			return m, true
		}
	}
	return mergeFact{}, false
}

// fromMainReds: the first red run on main after a merge, before the next merge, blames the
// merge's lane. A red before any merge, or the second in one interval, blames no one.
func (w *attrWorld) fromMainReds() []Attribution {
	reds := append([]RedRun(nil), w.in.MainReds...)
	sort.SliceStable(reds, func(i, j int) bool { return reds[i].At.Before(reds[j].At) })
	blamed := map[int]bool{}
	var out []Attribution
	for _, r := range reds {
		idx := -1
		for i, m := range w.merges {
			if !m.at.After(r.At) {
				idx = i
			}
		}
		if idx < 0 || blamed[idx] {
			continue
		}
		blamed[idx] = true
		m := w.merges[idx]
		key := "sha:" + m.head
		if m.pr != "" {
			key = prKey(m.pr)
		}
		out = append(out, Attribution{Lane: m.lane, Source: "main-red", Key: key})
	}
	return out
}

// MainRedsFromEvents are the red runs the log itself holds: a merge gate that blocked on
// the trunk. CI runs on main come from the host and are added by the caller.
func MainRedsFromEvents(events []tdd.Event) []RedRun {
	var out []RedRun
	for _, e := range events {
		if e.Kind != "merge_gate_result" || e.Verdict != "blocked" || !abTrunk(e.Lane) {
			continue
		}
		if at, err := time.Parse(time.RFC3339, e.At); err == nil {
			out = append(out, RedRun{At: at, Ref: "merge-gate"})
		}
	}
	return out
}
