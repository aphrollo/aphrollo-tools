package tdd

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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

// Badge colours: green for an armed gate, gray for one a session turned off,
// and a distinct colour for each suffix so the state reads without the word.
const (
	ansiReset     = "\x1b[0m"
	ansiGreen     = "\x1b[32m"
	ansiGray      = "\x1b[90m"
	ansiRed       = "\x1b[31m"
	ansiYellow    = "\x1b[33m"
	badgeOn       = "[aphrollo]"
	badgeOff      = "[aphrollo:off]"
	suffixRed     = "red"
	suffixDefer   = "deferred"
	suffixQueued  = "queued"
	suffixMutants = "mutants"
)

// StatusLine renders the one-line badge for a statusline payload. It never
// fails: it runs on every prompt render and has nowhere to report an error, so
// a malformed payload, a missing session or an unreadable log all render the
// plain armed badge.
func StatusLine(raw []byte) string {
	var in statusLineInput
	_ = json.Unmarshal(raw, &in)

	if s, _ := loadSession(in.SessionID); s != nil && s.Overrides.Off {
		return ansiGray + badgeOff + ansiReset
	}
	badge := ansiGreen + badgeOn + ansiReset
	colour, suffix := statusSuffix(in.SessionID, in.Cwd)
	if suffix == "" {
		return badge
	}
	return badge + " " + colour + suffix + ansiReset
}

// statusSuffix is the ONE extra word the badge may carry, in the order a
// session needs to act on it: a red suite is work to do now, a running
// deferred build is work to wait for, and a queued run is a suite that never
// ran at all. Everything else renders nothing.
func statusSuffix(session, cwd string) (colour, suffix string) {
	root := findRootFrom(cwd)
	if root == "" {
		return "", ""
	}
	if lastOutcomeIsRed(session, root) {
		return ansiRed, suffixRed
	}
	if deferredBuildRunning(root, time.Now()) {
		return ansiYellow, suffixDefer
	}
	if lastRunQueued(root) {
		return ansiYellow, suffixQueued
	}
	// Last, and deliberately: a mutation run is background work nobody is
	// blocked on. It is here at all because a session that cannot see it
	// starts a second one, or merges expecting a receipt that is still being
	// measured.
	if _, ok := MutantsJobRunningAt(root); ok {
		return ansiYellow, suffixMutants
	}
	return "", ""
}

// lastOutcomeIsRed reads the session's recorded outcome for THIS project. A
// red left in another repo earlier in the session is not this repo's state, so
// the lookup is keyed on the root the session is standing in.
func lastOutcomeIsRed(session, root string) bool {
	s, _ := loadSession(session)
	if s == nil {
		return false
	}
	ps, ok := s.ByProject[root]
	return ok && Outcome(ps.Outcome).IsRed()
}

// deferredBuildRunning reports whether a detached phase for root is still
// going. The result file's EXISTENCE is the liveness signal (the same rule the
// harvest uses — a pid can be reused and Windows cannot be signalled
// portably), and a job past the deferral ceiling is abandoned, not running.
func deferredBuildRunning(root string, now time.Time) bool {
	j, ok := loadDeferredJob(root)
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
