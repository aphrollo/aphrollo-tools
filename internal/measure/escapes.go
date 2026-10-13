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

// RedRun is a red CI run on the trunk, of the commit SHA whose title is Title.
type RedRun struct {
	At    time.Time
	SHA   string
	Title string
	Ref   string
}

// HostFacts is what the tracker and the CI host say of the escapes, read by the caller:
// the issues, a PR's head branch (nil asks nothing) and the red runs on the trunk.
type HostFacts struct {
	Issues   []EscapeIssue
	PRBranch func(pr int) (branch string, ok bool)
	MainReds []RedRun
}

// AttributionInput is everything the sources read. In keeps the records and red runs of
// the window; nil keeps all.
type AttributionInput struct {
	Events   []tdd.Event
	Issues   []EscapeIssue
	PRBranch func(pr int) (branch string, ok bool)
	MainReds []RedRun
	In       func(time.Time) bool
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
	lane string
	pr   string
	head string
	sha  string // the commit it made on the trunk, when the log holds it
}

// attrWorld is the input with its merges read once. merges are those of a lane; outside
// are the merges the log holds against the trunk itself (made outside the verb), which
// name no lane but may name the commit a red run is about.
type attrWorld struct {
	in      AttributionInput
	merges  []mergeFact
	outside []mergeFact
	// covered are the issues a record already counted.
	covered map[int]bool
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

func (w *attrWorld) in2(at string) bool {
	if w.in.In == nil {
		return true
	}
	t, err := time.Parse(time.RFC3339, at)
	return err == nil && w.in.In(t)
}

// AttributeEscapes folds the three sources into the escapes of each lane.
func AttributeEscapes(in AttributionInput) []Attribution {
	w := &attrWorld{in: in, covered: map[int]bool{}}
	for _, e := range in.Events {
		if e.Kind != "merge" || e.Verdict != "ok" || e.Lane == "" {
			continue
		}
		m := mergeFact{lane: e.Lane, pr: e.Detail["pr"], head: e.Detail["head"], sha: e.Detail["sha"]}
		if abTrunk(e.Lane) {
			w.outside = append(w.outside, m)
			continue
		}
		w.merges = append(w.merges, m)
	}
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

// laneOfPR is the lane a PR merged from: a merge of a lane the log holds, else the PR's
// head branch from the host. A merge logged against the trunk names no lane.
func (w *attrWorld) laneOfPR(pr string) (string, bool) {
	for _, m := range w.merges {
		if m.pr == pr {
			return m.lane, true
		}
	}
	if n, err := strconv.Atoi(pr); err == nil && w.in.PRBranch != nil {
		if b, ok := w.in.PRBranch(n); ok && !abTrunk(b) {
			return b, true
		}
	}
	return "", false
}

// fromRecords: `gate escape record` writes an escape event for the lane it ran in, or for
// the lane of the PR it names when it ran from the trunk. The issue the record opened is
// noted, so the issue source does not count it again.
func (w *attrWorld) fromRecords() []Attribution {
	issueOf := map[string]int{}
	for _, e := range w.in.Events {
		if e.Kind == "escape.issue" {
			if n, err := strconv.Atoi(e.Detail["number"]); err == nil {
				issueOf[e.Detail["id"]] = n
			}
		}
	}
	var out []Attribution
	for _, e := range w.in.Events {
		if e.Kind != "escape" || e.Verdict == "false-positive" || !w.in2(e.At) {
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
		if n, ok := issueOf[e.Detail["id"]]; ok {
			w.covered[n] = true
		}
		out = append(out, Attribution{Lane: lane, Source: "record", Key: key})
	}
	return out
}

var (
	prRef  = regexp.MustCompile(`(?i)\bPR\s*#?(\d+)|/pull/(\d+)|\(#(\d+)\)`)
	shaRef = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// fromIssues: an issue titled `escape:` names the PR or the merge sha that caused it. An
// issue a record already counted is not counted again.
func (w *attrWorld) fromIssues() []Attribution {
	var out []Attribution
	for _, is := range w.in.Issues {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(is.Title)), "escape:") || w.covered[is.Number] {
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
			if a, ok := w.blameSHA(sha, "issue"); ok {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// sameCommit reports whether two shas, either abbreviated, name one commit.
func sameCommit(a, b string) bool {
	return len(a) >= 7 && len(b) >= 7 && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

// blameSHA is the escape blamed on the merge that made, or whose PR head is, the commit sha.
func (w *attrWorld) blameSHA(sha, source string) (Attribution, bool) {
	for _, m := range append(append([]mergeFact(nil), w.merges...), w.outside...) {
		if !sameCommit(m.head, sha) && !sameCommit(m.sha, sha) {
			continue
		}
		lane, key := m.lane, "sha:"+sha
		if m.pr != "" {
			key = prKey(m.pr)
		}
		if abTrunk(lane) {
			l, ok := w.laneOfPR(m.pr)
			if m.pr == "" || !ok {
				continue
			}
			lane = l
		}
		return Attribution{Lane: lane, Source: source, Key: key}, true
	}
	return Attribution{}, false
}

// fromMainReds: a red CI run on main blames the merge that produced the commit it ran on:
// found by the commit's sha among the merges the log holds, else by the PR its title names.
// A red run of a commit no merge explains blames no one. A merge gate that blocked is no
// red run: it refused an incoming merge, and main stayed as it was.
func (w *attrWorld) fromMainReds() []Attribution {
	var out []Attribution
	for _, r := range w.in.MainReds {
		if w.in.In != nil && !w.in.In(r.At) {
			continue
		}
		if a, ok := w.blameSHA(r.SHA, "main-red"); ok {
			out = append(out, a)
			continue
		}
		if m := prRef.FindStringSubmatch(r.Title); m != nil {
			pr := m[1] + m[2] + m[3]
			if lane, ok := w.laneOfPR(pr); ok {
				out = append(out, Attribution{Lane: lane, Source: "main-red", Key: prKey(pr)})
			}
		}
	}
	return out
}
