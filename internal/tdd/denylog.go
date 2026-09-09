package tdd

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// An escape hatch nobody counts is an escape hatch nobody manages. The gate
// logged what it RAN — suites, stages, timeouts — and nothing it REFUSED, so
// a denied edit, a rejected commit message and a session that turned
// enforcement off all left the same trace: none. These helpers give each
// decision one gate.log line in the shape the stats parser already reads, so
// "which policy fires, how often" is a tally instead of an impression.

// LogEditDecision records an edit the gate DENIED, and every waiver an
// allowed edit claimed. An ordinary allowed edit writes nothing: the log must
// stay a record of decisions, not a transcript of keystrokes.
func LogEditDecision(raw []byte, d Decision) {
	if d.Action != Block && len(d.Escapes) == 0 {
		return
	}
	var in preToolUseInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return
	}
	root, rel := logPlace(editLogPath(in))
	if d.Action == Block {
		appendGateLog("preedit", root, rel, "pretooluse-denied:"+logToken(policyName(d)), 0)
	}
	for _, esc := range d.Escapes {
		appendGateLog("preedit", root, rel, logToken(esc), 0)
	}
}

// LogOverride is logOverride for a caller outside this package (the git
// shim) that made an override decision of its own — the discard wall's
// APHROLLO_DISCARD=1 and one-shot-arm passes, neither of which runs inside a
// hook that already has a Decision to log.
func LogOverride(verdict, session, cwd string) {
	logOverride(verdict, session, cwd)
}

// logOverride records a session flipping enforcement, in the project it was
// flipped in. The session id is the command, so a reader can tell one
// session's third `/gate off` from three sessions' first.
func logOverride(verdict, session, cwd string) {
	root := "-"
	if cwd != "" {
		if r := findRootFrom(cwd); r != "" {
			root = r
		} else {
			root = cwd
		}
	}
	appendGateLog("session", logToken(root), logToken(session), verdict, 0)
}

// policyName is the verdict's key. A decision that named no policy still gets
// counted — under a name that says the gate could not attribute it, which is
// itself worth seeing in the tally.
func policyName(d Decision) string {
	if d.Policy != "" {
		return d.Policy
	}
	return "unnamed"
}

// logPlace splits an edited path into the (root, path-within-root) pair a log
// line carries. Neither field may be empty: the parser reads the line by
// field position, so a missing one shifts every field after it.
func logPlace(path string) (root, rel string) {
	if path == "" {
		return "-", "-"
	}
	dir := filepath.Dir(path)
	root = RepoRoot(dir)
	if root == "" {
		root = dir
	}
	rel = path
	if r, err := filepath.Rel(root, path); err == nil {
		rel = r
	}
	return logToken(root), logToken(rel)
}

// LogToken is logToken for a caller outside this package that appends its
// own field (a cwd, an argv, a reason) to a gate.log line via AppendGateLog
// and must keep that field whitespace-safe the same way this package's own
// callers do.
func LogToken(s string) string {
	return logToken(s)
}

// logToken makes one field safe for a whitespace-separated log line: the
// stats parser reads fields, and a pattern or path carrying a space would
// silently shift the verdict column.
func logToken(s string) string {
	if s = strings.Join(strings.Fields(s), "_"); s == "" {
		return "-"
	}
	return s
}
