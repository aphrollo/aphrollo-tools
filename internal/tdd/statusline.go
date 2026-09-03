package tdd

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The statusline is the one surface a session sees on EVERY prompt render, so
// it says as little as possible: whether the gate is armed, and the one fact
// that changes what the session should do next. Everything else is already in
// the hook's own line, which is louder and closer to the edit.

// statusLineInput is the subset of Claude Code's statusline payload the badge
// needs: which session's overrides and outcomes to read, and where the session
// is standing.
type statusLineInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// The BADGE carries the state, in its own colour: green armed, red for a
// standing failure, yellow while something this session started is still
// running, gray for a gate the session turned off. A TAG is added inside the
// brackets only where the colour alone is ambiguous -- yellow has three
// causes, so it names which; red, green and gray have one each, so they render
// the bare badge. A word the colour already carries is a word a session stops
// reading, and a badge that changes width every render is one that draws the
// eye for nothing.
const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiGray   = "\x1b[90m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	badgeOn    = "[aphrollo]"
	tagDefer   = "deferred"
	tagQueued  = "queued"
	tagMutants = "mutants"
)

// redGoesStaleAfter bounds how long a recorded red may speak for the tree with
// nothing after it. The badge is a real-time signal or it is noise: a red from
// an hour ago describes code the session has long since moved past, and one
// false red teaches a reader to ignore the true one. Nothing is rendered in
// its place -- a `stale` word would be the same false claim, spelled longer.
const redGoesStaleAfter = 30 * time.Minute

// StatusLine renders the one-line badge for a statusline payload. It never
// fails: it runs on every prompt render and has nowhere to report an error, so
// a malformed payload, a missing session or an unreadable log all render the
// plain armed badge.
func StatusLine(raw []byte) string {
	var in statusLineInput
	_ = json.Unmarshal(raw, &in)

	if s, _ := loadSession(in.SessionID); s != nil && s.Overrides.Off {
		return ansiGray + badgeOn + ansiReset
	}
	colour, tag := statusState(in.SessionID, in.Cwd)
	if tag == "" {
		return colour + badgeOn + ansiReset
	}
	return colour + "[aphrollo:" + tag + "]" + ansiReset
}

// statusState is the badge's colour and, where the colour is ambiguous, its
// one tag -- in the order a session needs to act on them: a standing red is
// work to do now, a running job is work to wait for, and a queued run is a
// suite that never ran at all.
func statusState(session, cwd string) (colour, tag string) {
	root := findRootFrom(cwd)
	if root == "" {
		return ansiGreen, ""
	}
	now := time.Now()
	if redStands(session, root, now) {
		return ansiRed, ""
	}
	if deferredBuildRunning(session, root, now) {
		return ansiYellow, tagDefer
	}
	if mutantsRunning(session, root) {
		return ansiYellow, tagMutants
	}
	if lastRunQueued(root) {
		return ansiYellow, tagQueued
	}
	return ansiGreen, ""
}

// redStands reports whether the session's recorded red for THIS project is
// still the truth about the tree. Three things retire it, and the badge reads
// state the hooks already wrote rather than running anything itself:
//
//   - a red in another repo is not this repo's state, so the lookup is keyed
//     on the root the session is standing in;
//   - ANY green outcome logged for this project at or after the red clears it,
//     whatever stage produced it -- a fix that lands through a commit is
//     proven by the commit gate, and waiting for the next post-edit run to
//     believe it leaves the badge lying for as long as the session keeps
//     committing;
//   - a red past redGoesStaleAfter with nothing after it speaks for a tree
//     nobody has measured recently, and is dropped.
func redStands(session, root string, now time.Time) bool {
	s, _ := loadSession(session)
	if s == nil {
		return false
	}
	ps, ok := s.ByProject[root]
	if !ok || !Outcome(ps.Outcome).IsRed() {
		return false
	}
	at, err := time.Parse(time.RFC3339, ps.TS)
	if err != nil || now.Sub(at) > redGoesStaleAfter {
		return false
	}
	return !greenLoggedSince(root, at)
}

// greenLoggedSince reports whether any gate stage logged a green outcome for
// this project at or after `at`. The gate log is the one record every stage
// writes -- post-edit, pre-commit, pre-merge-commit and the post-Bash harvest
// alike -- so reading it is how the badge sees a green it was not present for.
// `at` is inclusive: the log stamps whole seconds, and a commit gate that
// cleared a red within the same second still cleared it.
func greenLoggedSince(root string, at time.Time) bool {
	dir := stateDir()
	if dir == "" {
		return false
	}
	f, err := os.Open(filepath.Join(dir, "gate.log"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || e.at.Before(at) || !sameProject(e.root, root) {
			continue
		}
		if strings.HasPrefix(e.verdict, "green") {
			return true
		}
	}
	return false
}

// sameProject matches a gate-log root against the root the badge is rendering
// for. A stage logs the root it RAN in, which for a cargo workspace member is
// a directory inside the project, so a nested root counts as this project.
func sameProject(logged, root string) bool {
	logged, root = filepath.Clean(logged), filepath.Clean(root)
	if runtime.GOOS == "windows" {
		logged, root = strings.ToLower(logged), strings.ToLower(root)
	}
	if logged == root {
		return true
	}
	return strings.HasPrefix(logged, root+string(filepath.Separator))
}

// mutantsRunning reports whether a cargo-mutants run this session started is
// still holding this project's target dir. The build-slot owner record is the
// live evidence -- it is written when the slot is taken and removed when it is
// released -- so the badge learns about a job that outlives the hook that
// launched it without probing a pid. A record naming a DIFFERENT session is
// another session's work and not this badge's business.
func mutantsRunning(session, root string) bool {
	o, ok := ReadBuildSlotOwner(resolveTargetDir(os.Getenv, root))
	if !ok {
		return false
	}
	if o.SessionID != "" && session != "" && o.SessionID != session {
		return false
	}
	return strings.Contains(strings.ToLower(o.Cmd), "mutants")
}

// deferredBuildRunning reports whether THIS session's detached phase for root
// is still going. Another session's build is not this badge's business: it is
// not work this session can wait for or act on. The result file's EXISTENCE is the liveness signal (the same rule the
// harvest uses — a pid can be reused and Windows cannot be signalled
// portably), and a job past the deferral ceiling is abandoned, not running.
func deferredBuildRunning(session, root string, now time.Time) bool {
	j, ok := loadDeferredJob(session, root)
	if !ok || deferredExpired(j, now) {
		return false
	}
	_, done := deferredResult(j)
	return !done
}

// lastRunQueued reports whether the most recent gate run for root ended
// QUEUED-SKIPPED: the suite never started, which is the outcome most easily
// mistaken for a quiet green. A later run of any kind clears it.
func lastRunQueued(root string) bool {
	dir := stateDir()
	if dir == "" {
		return false
	}
	f, err := os.Open(filepath.Join(dir, "gate.log"))
	if err != nil {
		return false
	}
	defer f.Close()
	last := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if e, ok := parseGateLine(sc.Text()); ok && e.root == root {
			last = e.verdict
		}
	}
	return last == "queued-skipped"
}
