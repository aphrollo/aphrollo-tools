package tdd

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// The escape loop had one hole: nothing recorded a miss AUTOMATICALLY. Every
// record came from somebody typing `gate escape record` in the minute after
// being annoyed, which is precisely the minute nobody does paperwork. So the
// four moments the gate can recognise a miss by itself now record one.
//
//	(a) the merge gate refusing a lane the commit gate passed on the same tree
//	(b) CI red on a tip whose commit carries the local gate's green trailer
//	(c) a mutation receipt reaching the merge with unaccepted survivors
//	(d) an override — the gate turned off, a check talked past — which is the
//	    same loop pointing the other way, and is opened by `escape sync`
//	    rather than immediately, because one annoyed session is not evidence.
//
// Each is deduped by a FINGERPRINT (the stage plus the first diagnostic line,
// hashed) inside a seven-day window: a recurring red must be one issue, not
// one issue per run, and after a week the same class escaping again is news.

// escapeDedupeWindow is how long one fingerprint stands for its class.
const escapeDedupeWindow = 7 * 24 * time.Hour

// escapeFingerprint identifies a MISS, not an occurrence: the stage that
// missed it plus the first line of the diagnostic. Later lines carry paths,
// timings and counts that differ run to run, and folding them in would defeat
// the dedupe entirely.
func escapeFingerprint(stage string, o EscapeOptions) string {
	diag := firstLine(strings.TrimSpace(o.Evidence))
	if diag == "" {
		diag = firstLine(strings.TrimSpace(o.Reason))
	}
	sum := sha256.Sum256([]byte(stage + "\n" + diag))
	return hex.EncodeToString(sum[:8])
}

// recordEscapeOnce records unless an OPEN record with the same fingerprint is
// younger than the window. A CLOSED record never suppresses: a hole that
// reopens after somebody fixed it is the loudest signal this loop produces.
func recordEscapeOnce(stage string, o EscapeOptions, w io.Writer) (EscapeRecord, bool) {
	o.Fingerprint = escapeFingerprint(stage, o)
	if seen, ok := recentEscape(o.Fingerprint); ok {
		return seen, false
	}
	r, err := RecordEscape(o, w)
	if err != nil {
		fmt.Fprintf(w, "gate: could not record the escape (%v)\n", err)
		return EscapeRecord{}, false
	}
	return r, true
}

// recentEscape finds an open record of the same class inside the window.
func recentEscape(fingerprint string) (EscapeRecord, bool) {
	if fingerprint == "" {
		return EscapeRecord{}, false
	}
	cutoff := time.Now().UTC().Add(-escapeDedupeWindow)
	for _, r := range loadEscapes() {
		if r.Closed || r.Fingerprint != fingerprint {
			continue
		}
		if r.At.After(cutoff) {
			return r, true
		}
	}
	return EscapeRecord{}, false
}

// --- the channel: what the local gate tells a machine it will never meet ----

// A CI runner has never seen this box's gate state, so it cannot know whether
// the local gate ever passed on the commit it is failing — and without that,
// a CI red is just a CI red, not evidence about the gate. The smallest thing
// that carries the fact is the COMMIT ITSELF: the commit-msg hook appends one
// trailer naming the tree the pre-commit gate passed every stage on.
//
// Smaller than the two obvious alternatives, and that is why it is the one
// chosen: a PR body has to be written by somebody and can be edited afterwards,
// and a check-run annotation costs an API call plus a token with checks:write.
// A trailer costs nothing, travels with the commit through push, fork and
// rebase-less merge, and is verifiable — it names the tree, so it cannot be
// copied onto a commit whose content differs.

// gateTrailerKey is the trailer's key, in git's own trailer shape.
const gateTrailerKey = "Gate:"

// gateGreenTrailer is the line the commit-msg hook writes.
func gateGreenTrailer(tree string) string {
	return gateTrailerKey + " green " + tree
}

// precommitGreenFile remembers the tree the last pre-commit gate passed, one
// per repo. It exists only to survive the few milliseconds between pre-commit
// and commit-msg, which are separate processes.
func precommitGreenFile(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	return gcStatePath("precommit-green." + repoStateKey(repoRoot) + ".txt")
}

// stampPrecommitGreen records that every pre-commit stage passed for the tree
// currently in the index. `git write-tree` names exactly the tree the commit
// about to be created will have, so the stamp cannot vouch for anything else.
func stampPrecommitGreen(repoRoot string) {
	path := precommitGreenFile(repoRoot)
	tree := indexTree(repoRoot)
	if path == "" || tree == "" {
		return
	}
	_ = os.WriteFile(path, []byte(tree), 0o600)
}

// StampPrecommitGreen is the hook-side spelling: the commit gate calls it
// after it has allowed a commit.
func StampPrecommitGreen(repoRoot string) { stampPrecommitGreen(repoRoot) }

// indexTree is the tree the staged index would commit as. It WRITES tree
// objects into the object database, which is exactly what `git commit` is
// about to do anyway, so it adds no garbage a commit would not.
func indexTree(repoRoot string) string {
	out, err := git(repoRoot, "write-tree")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// AppendGateTrailer adds the green-gate trailer to the message being written,
// but only when the pre-commit gate passed every stage for THIS tree. A stamp
// left by an earlier commit names a different tree and vouches for nothing.
// Best-effort throughout: this is a channel, not a check, and a commit must
// never fail because a trailer could not be written.
func AppendGateTrailer(repoRoot, msgPath string) {
	path := precommitGreenFile(repoRoot)
	if path == "" || msgPath == "" {
		return
	}
	stamped, err := os.ReadFile(path)
	if err != nil {
		return
	}
	tree := indexTree(repoRoot)
	if tree == "" || strings.TrimSpace(string(stamped)) != tree {
		return
	}
	data, err := os.ReadFile(msgPath)
	if err != nil {
		return
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.Contains(text, gateTrailerKey+" green ") {
		return // an amend re-runs the hook; one trailer is enough
	}
	// A trailer block is separated from the body by a blank line, and git's
	// own trailer parser will not see it otherwise.
	text = strings.TrimRight(text, "\n") + "\n\n" + gateGreenTrailer(tree) + "\n"
	_ = os.WriteFile(msgPath, []byte(text), 0o600)
}

// commitCarriesGreenGate reports whether rev's message claims the local gate
// passed, for rev's OWN tree. The tree comparison is what makes the claim
// unforgeable by copy-paste: a trailer moved to a commit with different
// content names the wrong tree and is ignored.
func commitCarriesGreenGate(repoRoot, rev string) bool {
	msg, err := git(repoRoot, "log", "-1", "--format=%B", rev)
	if err != nil {
		return false
	}
	tree, ok := revTree(repoRoot, rev)
	if !ok {
		return false
	}
	return strings.Contains(strings.ReplaceAll(msg, "\r\n", "\n"), gateGreenTrailer(tree))
}

// --- trigger (a) and (c): the merge gate ------------------------------------

// unacceptedSurvivorMarker is the phrase the receipt gate's own rejection
// uses. A pinned test builds that rejection and asserts the predicate reads
// it, so a reworded sentence fails loudly instead of silently killing the
// trigger.
const unacceptedSurvivorMarker = "unaccepted survivor"

func isUnacceptedSurvivorRejection(message string) bool {
	return strings.Contains(message, unacceptedSurvivorMarker)
}

// NoteMergeGateEscape records the merge gate's rejection as an escape when the
// rejection is evidence about the GATE rather than about the lane:
//
//   - unaccepted mutation survivors: a code path no test constrains reached
//     the last gate that could stop it, and no earlier stage judges survivors;
//   - a lane whose pre-commit gate passed every stage on the same tree: two
//     gates disagreed about one tree, so the cheaper one is missing a stage.
//
// Every other rejection is the gate WORKING, and recording those would bury
// the evidence in noise. Best-effort and silent on failure: the merge has
// already been refused by the time this runs, and its verdict is not this
// function's to change.
func NoteMergeGateEscape(repoRoot, message string, w io.Writer) {
	stage, reason := "", ""
	switch {
	case isUnacceptedSurvivorRejection(message):
		stage = "merge:mutation-receipt"
		reason = "a lane reached the merge gate with unaccepted mutation survivors — a code path no test constrains"
	case mergeTipCarriesGreenGate(repoRoot):
		stage = "merge:premergecommit"
		reason = "the merge gate refused a lane whose pre-commit gate had passed every stage on the same tree"
	default:
		return
	}
	recordEscapeOnce(stage, EscapeOptions{
		Reason:   reason,
		Kind:     EscapeKind,
		Evidence: message,
		Repo:     repoRoot,
		Check:    stage,
	}, w)
}

// mergeTipCarriesGreenGate reports whether the lane being merged claims a
// green local gate for its own tree.
func mergeTipCarriesGreenGate(repoRoot string) bool {
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		return false
	}
	return commitCarriesGreenGate(repoRoot, tip.Rev)
}

// --- trigger (b): CI ---------------------------------------------------------

// RecordCIEscape is what the CI job runs when a workflow fails: it records an
// escape ONLY when the tip it failed on carries the local gate's green
// trailer for its own tree. Without that, CI red says nothing about the gate
// — it says the gate never ran — and recording it would fill the loop with
// pushes nobody gated. The bool says whether anything was recorded.
func RecordCIEscape(repo, job, evidence string, w io.Writer) (EscapeRecord, bool) {
	if !commitCarriesGreenGate(repo, "HEAD") {
		fmt.Fprintf(w, "gate escape: HEAD carries no green gate trailer, so this CI failure is not evidence about the gate — nothing recorded\n")
		return EscapeRecord{}, false
	}
	return recordEscapeOnce("ci:"+job, EscapeOptions{
		Reason:   fmt.Sprintf("%s failed on a tip the local gate passed green", job),
		Kind:     EscapeKind,
		FromCI:   job,
		Evidence: evidence,
		Repo:     repo,
	}, w)
}

// --- trigger (d): the overrides ---------------------------------------------

// OverrideCandidate is one false-positive candidate the log carries.
type OverrideCandidate struct {
	// Stage names the override, and is what the fingerprint keys on.
	Stage    string
	Reason   string
	Evidence string
}

// overrideVerdictPrefixes are the log verdicts that record a session going
// AROUND a check rather than a check refusing something. `override-off` is
// `/gate off`; the primary-edits prefix covers the primary-checkout override
// however the guard that writes it spells its verdict.
//
// `git commit --no-verify` is deliberately absent and cannot be added here: it
// skips the hooks entirely, so there is no line for a scanner to find. The
// only trace it could leave is an absence, and an absence is indistinguishable
// from a commit made on another box.
var overrideVerdictPrefixes = []string{"override-off", "primary-edits"}

// OverrideCandidates reads gate.log for evidence that a check was gone around
// inside the window: a session that turned the gate off, an edit that a
// primary-checkout override let through, and — the sharpest of the three — a
// check that DENIED an edit which then went in on a waiver for the same file.
//
// It names candidates, not verdicts. The answer may be to fix the check, to
// narrow it, or to demote it, and only a human reading the issue can tell
// which; `escape sync` is what opens them, so one annoyed afternoon does not
// open an issue mid-session.
func OverrideCandidates(r io.Reader, now time.Time) []OverrideCandidate {
	cutoff := now.Add(-escapeDedupeWindow)
	// denied[root+file] is the check that refused an edit there, still
	// waiting to see whether the edit went in anyway.
	denied := map[string]string{}
	var out []OverrideCandidate
	seen := map[string]bool{}
	add := func(c OverrideCandidate) {
		if seen[c.Stage] {
			return
		}
		seen[c.Stage] = true
		out = append(out, c)
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		e, ok := parseGateLine(line)
		if !ok || e.at.Before(cutoff) || e.at.After(now.Add(time.Hour)) {
			continue
		}
		file := gateLineFile(line)
		switch {
		case strings.HasPrefix(e.verdict, "pretooluse-denied:"):
			denied[e.root+"\x00"+file] = strings.TrimPrefix(e.verdict, "pretooluse-denied:")
		case strings.HasPrefix(e.verdict, "smell-escape:"):
			check, refused := denied[e.root+"\x00"+file]
			if !refused {
				continue
			}
			add(OverrideCandidate{
				Stage:  "override:" + check,
				Reason: fmt.Sprintf("%s refused an edit that then went in on a waiver — narrow it, fix it, or demote it", check),
				Evidence: fmt.Sprintf("gate.log: pretooluse-denied:%s on %s, then %s on the same file inside %d days",
					check, file, e.verdict, int(escapeDedupeWindow.Hours()/24)),
			})
		default:
			for _, p := range overrideVerdictPrefixes {
				if !strings.HasPrefix(e.verdict, p) {
					continue
				}
				add(OverrideCandidate{
					Stage:    "override:" + e.verdict,
					Reason:   fmt.Sprintf("a session went around the gate (%s) — a check that gets switched off is a check to narrow or fix", e.verdict),
					Evidence: "gate.log: " + line,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stage < out[j].Stage })
	return out
}

// gateLineFile is the log line's FILE field — the one after the root. It is
// read positionally, like the rest of the line, and "" for a line too short
// to have one.
func gateLineFile(line string) string {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 5 {
		return ""
	}
	return f[3]
}

// RecordOverrideCandidates records each candidate as a false positive,
// deduped by fingerprint, and returns how many were newly recorded. Called by
// `escape sync`: an override is a prompt to look at a check, and the looking
// happens on somebody's own schedule, not in the middle of the session that
// was annoyed.
func RecordOverrideCandidates(repo string, candidates []OverrideCandidate, w io.Writer) int {
	opened := 0
	for _, c := range candidates {
		r, recorded := recordEscapeOnce(c.Stage, EscapeOptions{
			Reason:   c.Reason,
			Kind:     FalsePositiveKind,
			Evidence: c.Evidence,
			Repo:     repo,
			Check:    strings.TrimPrefix(c.Stage, "override:"),
		}, w)
		if !recorded {
			continue
		}
		opened++
		fmt.Fprintf(w, "override-candidate %s recorded as %s\n", c.Stage, r.ID)
	}
	return opened
}
