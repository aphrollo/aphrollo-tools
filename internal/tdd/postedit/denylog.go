package postedit

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
		AppendGateLogDetail("preedit", root, rel, "pretooluse-denied:"+LogToken(policyName(d)), 0, denyDetail(d))
	}
	for _, esc := range d.Escapes {
		AppendGateLog("preedit", root, rel, LogToken(esc), 0)
	}
}

// LogOverride records a session flipping enforcement, in the project it was
// flipped in. The session id is the command, so a reader can tell one
// session's third `/gate off` from three sessions' first. Exported for a
// caller outside this package (the git shim) that made an override decision
// of its own — the discard wall's APHROLLO_DISCARD=1 and one-shot-arm
// passes, neither of which runs inside a hook that already has a Decision to
// log.
func LogOverride(verdict, session, cwd string) {
	root := "-"
	if cwd != "" {
		if r := findRootFrom(cwd); r != "" {
			root = r
		} else {
			root = cwd
		}
	}
	AppendGateLog("session", root, LogToken(session), verdict, 0)
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
	return root, LogToken(rel)
}

// wallPolicies are the policies that judge where or how a write lands, not what
// it says: a deny from one of them is a wall, not a smell.
var wallPolicies = map[string]bool{
	primaryCheckoutPolicy: true, discardBashPolicy: true, directPROpenPolicy: true, undercoverBashPolicy: true,
}

// denyDetail is what a deny event carries beside the rule the verdict names:
// the family the rule belongs to (law, wall or smell) and the override the
// decision offered, "none" when it offered none.
func denyDetail(d Decision) map[string]string {
	cause := "smell"
	switch {
	case strings.HasPrefix(d.Policy, "ratchet:"):
		cause = "law"
	case strings.HasPrefix(d.Policy, "bash-"), wallPolicies[d.Policy]:
		cause = "wall"
	}
	override := d.Override
	if override == "" {
		override = "none"
	}
	return map[string]string{"cause": cause, "override": override}
}
