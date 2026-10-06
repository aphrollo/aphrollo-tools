package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A transcript record's cwd names the primary checkout whatever lane the agent
// works on, so the lane of a turn is reconstructed by joining the transcript's
// (session, agent) with the event log's Actor, which the hooks write as
// "session" for the coordinator and "session/agent" for a subagent.
//
// What the log's Actor carries, checked against a real log: every kind of event
// has it, but only hook.timing events carry the agent part. An edit, a commit
// gate result, a push, a merge or a PR carries the session id alone, so it may be
// the coordinator's or a subagent's. A subagent's lane is therefore read from
// its own agent-keyed events alone; the coordinator's from the kinds below,
// which a builder does not run, never from edits and commits.

// actorKey is who spent the tokens: a session and, for a subagent, its agent.
type actorKey struct{ session, agent string }

// laneEvent is a moment a lane is named for an actor; lane "" is a worktree
// left (ExitWorktree), after which no lane is named until the next one.
type laneEvent struct {
	at   time.Time
	lane string
}

// laneReach is how far from a lane event a coordinator's turn is still on that
// lane: it merged or pushed or entered the lane a moment ago, or is about to.
const laneReach = 30 * time.Minute

// coordinatorKinds are the log events that name a lane for the coordinator.
var coordinatorKinds = map[string]bool{"merge": true, "pr_opened": true, "push": true}

// The rows of the per-lane table that are no lane.
const (
	laneCoordination = "coordination"
	laneUnattributed = "unattributed"
)

// addLogEvents joins the event log: an event with an agent part is that
// subagent's lane event whatever its kind; one without is the coordinator's
// only for the kinds a coordinator runs.
func (s *scan) addLogEvents() {
	for _, e := range s.o.Events {
		if e.Lane == "" || e.Lane == "main" || e.Actor == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil {
			continue
		}
		session, agent, _ := strings.Cut(e.Actor, "/")
		if agent == "" && !coordinatorKinds[e.Kind] {
			continue
		}
		k := actorKey{session, agent}
		s.lanes[k] = append(s.lanes[k], laneEvent{at, e.Lane})
	}
}

// worktreeCalls reads the EnterWorktree and ExitWorktree tool calls of an
// assistant record: the tool name and the worktree's name or path, nothing else
// of the input.
func (s *scan) worktreeCalls(rec transcriptLine, actor actorKey, at time.Time) {
	content := rec.Message.Content
	if len(content) == 0 || content[0] != '[' {
		return
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		switch b.Name {
		case "ExitWorktree":
			s.lanes[actor] = append(s.lanes[actor], laneEvent{at, ""})
		case "EnterWorktree":
			var in struct {
				Name string `json:"name"`
				Path string `json:"path"`
			}
			if json.Unmarshal(b.Input, &in) != nil {
				continue
			}
			name := in.Name
			if name == "" && in.Path != "" {
				name = filepath.Base(filepath.FromSlash(strings.ReplaceAll(in.Path, "\\", "/")))
			}
			if name != "" {
				s.lanes[actor] = append(s.lanes[actor], laneEvent{at, "lane/" + name})
			}
		}
	}
}

// laneOf is the lane a turn belongs to. A subagent whose lane events all name
// one lane gives all its usage to it. Otherwise a turn is on the lane of its
// actor's latest lane event at or before it (a worktree left ends it), else the
// next one after it; a coordinator's turn only within laneReach of one. A
// coordinator with lane events but none in reach is coordination; an actor with
// no lane events at all, or a subagent none of whose events reach the turn, is
// unattributed.
func (s *scan) laneOf(t turn) string {
	evs := s.sorted(t.actor)
	sub := t.actor.agent != ""
	if lane := onlyLane(evs); sub && lane != "" {
		return lane
	}
	i := sort.Search(len(evs), func(i int) bool { return evs[i].at.After(t.at) })
	near := func(e laneEvent) bool {
		d := e.at.Sub(t.at)
		if d < 0 {
			d = -d
		}
		return sub || d <= laneReach
	}
	if i > 0 && evs[i-1].lane != "" && near(evs[i-1]) {
		return evs[i-1].lane
	}
	for _, e := range evs[i:] {
		if e.lane != "" {
			if near(e) {
				return e.lane
			}
			break
		}
	}
	if !sub && len(evs) > 0 {
		return laneCoordination
	}
	return laneUnattributed
}

// sorted is the actor's lane events in time order, sorted once.
func (s *scan) sorted(k actorKey) []laneEvent {
	evs := s.lanes[k]
	if !sort.SliceIsSorted(evs, func(i, j int) bool { return evs[i].at.Before(evs[j].at) }) {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].at.Before(evs[j].at) })
	}
	return evs
}

// cwdResolver maps the working directory a record names to the repository and
// lane it is in, by reading the checkout itself and not by guessing from the
// transcript directory's name.
type cwdResolver struct{ seen map[string][2]string }

func newCwdResolver() *cwdResolver { return &cwdResolver{seen: map[string][2]string{}} }

func (c *cwdResolver) resolve(cwd string) (repo, lane string) {
	if v, ok := c.seen[cwd]; ok {
		return v[0], v[1]
	}
	repo, lane = resolveCwd(cwd)
	c.seen[cwd] = [2]string{repo, lane}
	return repo, lane
}

const maxWalk = 16

// resolveCwd reads the repository and lane of a working directory. A path under
// .worktrees/<repo>/<lane> whose lane directory is gone says so itself, and is read first: such a path
// would otherwise resolve to whatever repository encloses it. Otherwise the checkout's .git is
// found by walking up: a directory is the main checkout (lane main); a file
// names a linked worktree, whose common dir's parent is the repo and whose own
// directory name is the lane.
func resolveCwd(cwd string) (repo, lane string) {
	if cwd == "" {
		return "", ""
	}
	if repo, lane, ok := worktreesShape(cwd); ok {
		return repo, lane
	}
	dir := cwd
	for i := 0; i < maxWalk; i++ {
		if repo, lane, ok := checkoutAt(dir); ok {
			return repo, lane
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	parts := strings.Split(strings.ReplaceAll(cwd, "\\", "/"), "/")
	return parts[len(parts)-1], "main"
}

func worktreesShape(cwd string) (repo, lane string, ok bool) {
	parts := strings.Split(strings.ReplaceAll(cwd, "\\", "/"), "/")
	// The innermost .worktrees names the lane the path is in.
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if p == ".worktrees" && i+2 < len(parts) {
			// A lane directory that still exists is read from its own .git, which is
			// exact; the path's shape names the lane of one that was removed.
			if _, err := os.Stat(filepath.FromSlash(strings.Join(parts[:i+3], "/"))); err == nil {
				return "", "", false
			}
			return parts[i+1], "lane/" + parts[i+2], true
		}
	}
	return "", "", false
}

func checkoutAt(dir string) (repo, lane string, ok bool) {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Lstat(gitPath)
	if err != nil {
		return "", "", false
	}
	if fi.IsDir() {
		return filepath.Base(dir), "main", true
	}
	data, err := os.ReadFile(gitPath)
	gitdir, found := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if err != nil || !found {
		return "", "", false
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	common := filepath.Join(gitdir, "..", "..")
	if rel, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common = strings.TrimSpace(string(rel))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
	}
	return filepath.Base(filepath.Dir(filepath.Clean(common))), "lane/" + filepath.Base(dir), true
}

// onlyLane is the one lane every event names, "" when they name none or several.
func onlyLane(evs []laneEvent) string {
	only := ""
	for _, e := range evs {
		switch {
		case e.lane == "":
		case only == "":
			only = e.lane
		case only != e.lane:
			return ""
		}
	}
	return only
}
