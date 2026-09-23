package tdd

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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

// The BADGE carries the state, in its own colour: white for a gate that is on
// with nothing measured, green for a suite that passed, red for a standing
// failure, yellow while something this session started is still running, gray
// for a gate the session turned off. A TAG is added inside the brackets where
// the colour alone is not enough -- yellow has three causes, so it names
// which, and OFF says so in text because a colour-stripped badge must never
// read as armed. White, red and green are colour-only: a word the colour
// already carries is a word a session stops reading, and all three of them
// mean the gate is running.
const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiGray   = "\x1b[90m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiWhite  = "\x1b[37m"
	badgeOn    = "[aphrollo]"
	tagOff     = "off"
	tagDefer   = "deferred"
	tagQueued  = "queued"
	tagMutants = "mutants"
)

// StatusLine renders the one-line badge for a statusline payload. It never
// fails: it runs on every prompt render and has nowhere to report an error, so
// a malformed payload, a missing session or an unreadable log all render the
// plain armed badge.
func StatusLine(raw []byte) string {
	var in statusLineInput
	_ = json.Unmarshal(raw, &in)

	if s, _ := loadSession(in.SessionID); s != nil && s.Overrides.Off {
		return badge(ansiGray, tagOff)
	}
	colour, tag := statusState(in.SessionID, in.Cwd)
	return badge(colour, tag)
}

// badge renders the one shape: `[aphrollo]` when the colour says everything,
// `[aphrollo:<tag>]` when it does not. OFF always carries its tag -- colour
// alone cannot say "not gated" to a consumer that strips SGR, and reading an
// ungated tree as gated is the one mistake this badge must not enable. White,
// red and green are colour-only: all three of them mean the gate is running.
func badge(colour, tag string) string {
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
		return ansiWhite, ""
	}
	now := time.Now()
	if redStands(session, root, now) {
		return ansiRed, ""
	}
	if deferredBuildRunning(session, root, now) {
		return ansiYellow, tagDefer
	}
	if mutantsRunning(root) {
		return ansiYellow, tagMutants
	}
	if lastRunQueued(root) {
		return ansiYellow, tagQueued
	}
	// Green is the badge's fallback, so everything that is not a standing red
	// or a running job used to render exactly like a suite that had just
	// passed — a session that had recorded nothing, and one whose only red had
	// gone stale, included. That is failing open in a colour: the ABSENCE of a
	// measurement shown as a good one. It compounds with an abandoned deferred
	// job, which writes no outcome at all, so nothing turns the badge red while
	// a session's edits go untested. The gate is still armed, so the badge
	// stays on -- in white, which claims nothing, leaving green to mean the one
	// thing it should: a suite that ran and passed.
	if !greenRecorded(session, root) {
		return ansiWhite, ""
	}
	return ansiGreen, ""
}

// greenRecorded reports whether the last outcome this session recorded for the
// project it is standing in was a pass. Only a recorded green earns the bare
// badge; no history at all, and a red that has gone stale with nothing after
// it, are both states where the tree has not been measured recently and the
// badge must say so.
func greenRecorded(session, root string) bool {
	s, _ := loadSession(session)
	if s == nil {
		return false
	}
	if ps, ok := s.ByProject[root]; ok && isGreenVerdict(ps.Outcome) {
		return true
	}
	// The gate log is the record EVERY stage writes, which is why redStands
	// already reads it: a green this session was not present for still proves
	// the tree. Without this the badge would call a tree unproven that a
	// commit gate had just measured.
	return isGreenVerdict(lastVerdictFor(root))
}

// isGreenVerdict reports whether a recorded verdict means tests ran and
// passed. The green family only: `green`, `green-unconstrained` and
// `green-with-warnings`. `writing-test` PASSED WITH NO TEST EXECUTED, and
// `no-delta` failed against pre-existing failures, so neither one is a
// measurement of this tree and neither may earn the bare badge.
func isGreenVerdict(verdict string) bool {
	return strings.HasPrefix(verdict, string(Green))
}

// lastVerdictFor is the last verdict any stage logged for this project, or ""
// when the log has nothing to say about it.
func lastVerdictFor(root string) string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(dir, "gate.log"))
	if err != nil {
		return ""
	}
	defer f.Close()
	last := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if e, ok := parseGateLine(sc.Text()); ok && sameProject(e.Root, root) {
			last = e.Verdict
		}
	}
	return last
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
// twin: internal/tdd/statusline.go#lastRunQueued
func greenLoggedSince(root string, at time.Time) bool {
	dir := StateDir()
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
		if !ok || e.At.Before(at) || !sameProject(e.Root, root) {
			continue
		}
		if strings.HasPrefix(e.Verdict, "green") {
			return true
		}
	}
	return false
}

// mutantsRunning reports whether a mutation run is going for THIS working
// tree. The box-wide run lock's own owner record is the evidence: a
// measurement holds that lock from before its build starts until its last
// mutant is judged, and the record names the tree it is measuring.
//
// Scoped to the ROOT, not the repo: a run measuring the lane beside this one
// says nothing about this tree. An earlier version read the build slot's
// owner record instead, which named a target DIR -- with a shared
// CARGO_TARGET_DIR that is one directory for many projects, so a run anywhere
// on the box rendered here.
func mutantsRunning(root string) bool {
	o, ok := readBuildLockOwnerAt(mutantsRunLockOwnerPath())
	return ok && sameProject(o.Cwd, root)
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
	dir := StateDir()
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
		if e, ok := parseGateLine(sc.Text()); ok && sameProject(e.Root, root) {
			last = e.Verdict
		}
	}
	return last == "queued-skipped"
}
