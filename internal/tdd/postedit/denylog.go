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
		detail := denyDetail(d)
		detail["file"] = slashPath(rel)
		AppendGateLogDetail("preedit", root, rel, "pretooluse-denied:"+LogToken(policyName(d)), 0, detail)
	}
	for _, esc := range d.Escapes {
		AppendGateLogDetail("preedit", root, rel, LogToken(esc), 0, map[string]string{"file": slashPath(rel)})
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
	AppendGateLogDetail("session", root, LogToken(session), verdict, 0, map[string]string{"file": LogToken(session)})
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

// slashPath is a repo-relative path as the events spell it, with forward slashes
// whatever the host: the measures match a path of one event against another's,
// and a commit refusal names its files that way.
func slashPath(p string) string { return strings.ReplaceAll(p, "\\", "/") }

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

// logLawGuides records, one event each, the law and file of every finding the
// edit stage named without denying the write: the commit-time measure of
// refusals the edit check missed matches a commit's refused pairs against these
// and the deny events.
func logLawGuides(root, stage string, hits []LawFinding) {
	seen := map[string]bool{}
	for _, h := range hits {
		key := h.Law + "|" + h.File
		if seen[key] || h.File == "" {
			continue
		}
		seen[key] = true
		AppendEvent(Event{Kind: "guide", Root: root, Stage: stage, Detail: map[string]string{"rule": "ratchet:" + h.Law, "file": h.File}})
	}
}

// LogLawGuides records the findings of the pre-edit judge that no deny event
// carries: every one when the write went ahead, and every one but the law and
// file the deny event names when it was refused.
func LogLawGuides(raw []byte, final Decision, found []LawFinding) {
	if len(found) == 0 {
		return
	}
	var in preToolUseInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return
	}
	root, rel := logPlace(editLogPath(in))
	deniedFile := slashPath(rel)
	var rest []LawFinding
	for _, f := range found {
		// The deny event names one law at the edited file; only that pair is
		// already recorded.
		if final.Action == Block && final.Policy == "ratchet:"+f.Law && slashPath(f.File) == deniedFile {
			continue
		}
		rest = append(rest, f)
	}
	logLawGuides(root, "preedit", rest)
}
