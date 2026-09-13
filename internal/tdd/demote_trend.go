package tdd

import (
	"bufio"
	"io"
	"runtime"
	"sort"
	"strings"
	"time"
)

// The trend behind a demotion candidate is a RATE, and three things had to
// change before it could carry the claim it makes.
//
// A RAW weekly count measures the week, not the check. This box's own log
// grew from 21 active lanes three weeks ago to 214 and then 309: against that
// denominator every check refuses more every week, and two of the five names
// the raw reading produced were DOWN 5% and 21% per lane. So the count is
// divided by the lanes that were active in the same window and the same repo
// — a lane is one piece of work in flight, which is the closest thing the log
// records to an opportunity for any check to fire, and unlike a run count it
// does not rise because one lane re-ran its suite two hundred times.
//
// A check with no history in the oldest window has an EMPTY oldest week, and
// "rose two weeks running" is then satisfied by its own first refusal. Every
// ratchet law on this box went in on one day; a week later all of them were
// candidates. A check is only judged over windows it existed for.
//
// Refusals from DIFFERENT repos are different laws. Two of the loudest names
// in one real week (comment_hygiene, collection_bound) belong to a Rust repo
// whose laws this checkout does not carry, and a name that exists in both is
// still a different rule in each. The trend is per repo, and a refusal the
// log could not place — the pre-edit hook writes `-` when the tool payload
// carried no path — belongs to no repo at all, so it is counted nowhere.

// demoteTrend is one reading of the log: who was active, who refused, and
// when each check was first seen at all. Keyed by demoteKey, so two spellings
// of one path are one repo.
type demoteTrend struct {
	lanes  [3]map[string]map[string]bool
	denied [3]map[string]map[string]int
	// first is the earliest sighting of a (repo, check) pair ANYWHERE in the
	// log, including before the three windows — that is what distinguishes a
	// quiet week from a check that did not exist yet.
	first map[string]time.Time
	// spelling remembers the first form of each repo key seen, so the report
	// names the path the way the log wrote it.
	spelling map[string]string
}

// readDemoteTrend reads the whole log once.
func readDemoteTrend(r io.Reader, now time.Time) *demoteTrend {
	t := &demoteTrend{first: map[string]time.Time{}, spelling: map[string]string{}}
	for i := range t.lanes {
		t.lanes[i] = map[string]map[string]bool{}
		t.denied[i] = map[string]map[string]int{}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok {
			continue
		}
		repo, lane := demotePlace(e.root)
		if repo == "" {
			continue
		}
		key := demoteKey(repo)
		if _, seen := t.spelling[key]; !seen {
			t.spelling[key] = repo
		}
		check := demoteCheckName(e.verdict)
		if check != "" {
			if at, seen := t.first[key+"\x00"+check]; !seen || e.at.Before(at) {
				t.first[key+"\x00"+check] = e.at
			}
		}
		age := now.Sub(e.at)
		if age < 0 || age >= 3*demoteWeek {
			continue
		}
		w := int(age / demoteWeek)
		if t.lanes[w][key] == nil {
			t.lanes[w][key] = map[string]bool{}
		}
		t.lanes[w][key][demoteKey(lane)] = true
		if check == "" {
			continue
		}
		if t.denied[w][key] == nil {
			t.denied[w][key] = map[string]int{}
		}
		t.denied[w][key][check]++
	}
	t.fold()
	return t
}

// fold merges every repo key that lives INSIDE another one into it: gate.log
// records the root a run was measured at, which for a workspace repo is often
// a crate or module directory (`D:/Projects/borld/crates/client`) rather than
// the checkout. Left apart, one repo's weeks are split across a dozen keys and
// no window has enough data to read. A directory under a checkout the log
// already names is that checkout, and it contributes the checkout's own lane
// rather than a lane of its own.
func (t *demoteTrend) fold() {
	keys := map[string]bool{}
	for i := range t.lanes {
		for k := range t.lanes[i] {
			keys[k] = true
		}
	}
	into := map[string]string{}
	for k := range keys {
		target := ""
		for o := range keys {
			if o != k && strings.HasPrefix(k, o+"/") && (target == "" || len(o) < len(target)) {
				target = o
			}
		}
		if target != "" {
			into[k] = target
		}
	}
	for k, target := range into {
		for i := range t.lanes {
			for lane := range t.lanes[i][k] {
				if t.lanes[i][target] == nil {
					t.lanes[i][target] = map[string]bool{}
				}
				// A lane key that IS its repo key is the checkout itself, so
				// it folds into the target's own lane rather than adding one.
				if lane == k {
					lane = target
				}
				t.lanes[i][target][lane] = true
			}
			delete(t.lanes[i], k)
			for check, n := range t.denied[i][k] {
				if t.denied[i][target] == nil {
					t.denied[i][target] = map[string]int{}
				}
				t.denied[i][target][check] += n
			}
			delete(t.denied[i], k)
		}
		for pair, at := range t.first {
			k2, check, _ := strings.Cut(pair, "\x00")
			if k2 != k {
				continue
			}
			moved := target + "\x00" + check
			if prev, seen := t.first[moved]; !seen || at.Before(prev) {
				t.first[moved] = at
			}
			delete(t.first, pair)
		}
	}
}

// candidates names every (repo, check) whose refusals per active lane rose in
// BOTH of the last two weeks — two consecutive rises, so one busy week is not
// a signal — over windows the check and the repo were both present for.
// Sorted, so the report is stable.
func (t *demoteTrend) candidates(now time.Time) []DemoteCandidate {
	oldest := now.Add(-3 * demoteWeek)
	var out []DemoteCandidate
	for key, checks := range t.denied[0] {
		l0, l1, l2 := len(t.lanes[0][key]), len(t.lanes[1][key]), len(t.lanes[2][key])
		if l0 == 0 || l1 == 0 || l2 == 0 {
			// A window with no activity at all is no evidence. Treating it as
			// a zero would read a repo's first week on this box as a rise.
			continue
		}
		for check, c0 := range checks {
			if at, seen := t.first[key+"\x00"+check]; !seen || at.After(oldest) {
				continue
			}
			c1, c2 := t.denied[1][key][check], t.denied[2][key][check]
			if rateRose(c0, l0, c1, l1) && rateRose(c1, l1, c2, l2) {
				out = append(out, DemoteCandidate{Repo: t.spelling[key], Check: check})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Check < out[j].Check
	})
	return out
}

// rateRose reports whether a/la is strictly greater than b/lb, compared by
// cross-multiplication: the counts are small integers and the answer must not
// depend on how two divisions round.
func rateRose(a, la, b, lb int) bool {
	return a*lb > b*la
}

// demotePlace splits a logged root into the repo it belongs to and the lane
// the work was done in, by the layout `workspace create` builds
// (<parent>/.worktrees/<repo>/<lane>) and the older in-repo one
// (<repo>/.claude/worktrees/<lane>). An empty repo means the entry names no
// repo: `-`, or the .worktrees container itself, which is nobody's checkout.
func demotePlace(root string) (repo, lane string) {
	p := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(root), `\`, "/"), "/")
	if p == "" || p == "-" {
		return "", ""
	}
	// The git shim records the git dir, which is the same checkout.
	p = strings.TrimSuffix(p, "/.git")
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		switch strings.ToLower(seg) {
		case ".worktrees":
			if i+2 >= len(segs) {
				return "", ""
			}
			repo = strings.Join(append(append([]string{}, segs[:i]...), segs[i+1]), "/")
			return repo, repo + "/" + segs[i+2]
		case "worktrees":
			if i == 0 || strings.ToLower(segs[i-1]) != ".claude" || i+1 >= len(segs) {
				continue
			}
			repo = strings.Join(segs[:i-1], "/")
			return repo, repo + "/" + segs[i+1]
		}
	}
	return p, p
}

// demotePathGOOSFn names the platform whose path rules the trend folds by,
// read through a seam rather than off runtime directly so a test pins the
// OTHER host's branch — the model internal/cli/selfinstall.go's binGOOS set,
// and what the platform_seam law asks for.
var demotePathGOOSFn = func() string { return runtime.GOOS }

// demoteKey is the form two spellings of one path compare equal in. Windows
// paths are case-insensitive, so a log carrying both `D:\Projects\borld` and
// `d:/projects/borld` must read as one repo, not two halves of one trend —
// and half a trend is not a smaller signal, it is a window with no data in
// it, which the candidate rule refuses to judge at all.
func demoteKey(path string) string {
	if demotePathGOOSFn() == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// demoteSameRepo reports whether a candidate's repo is the checkout a caller
// is filing issues against. Either may be a lane of the other (the sync runs
// from a worktree; the candidate names the checkout), and demotePlace has
// already reduced a worktree path to the repo it belongs to.
func demoteSameRepo(root, repo string) bool {
	a, _ := demotePlace(root)
	b, _ := demotePlace(repo)
	if a == "" || b == "" {
		return false
	}
	ka, kb := demoteKey(a), demoteKey(b)
	return ka == kb || strings.HasPrefix(ka, kb+"/") || strings.HasPrefix(kb, ka+"/")
}
