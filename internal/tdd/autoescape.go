package tdd

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync/atomic"
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

// escapeFingerprint identifies a MISS, not an occurrence: the repo it
// happened in, the stage that missed it, and what actually failed. Timings,
// paths and counts differ run to run, and folding them in would defeat the
// dedupe entirely.
//
// The repo is in there because two repos on one box failing the same way are
// two misses; without it the first one to record `test` silences the rest for
// a week. And "what failed" is the FAILING TEST NAMES wherever the evidence
// carries them, because the diagnostics that matter here open with a
// constant: every mechanical rejection begins "TDD mechanical: tests failing
// — fix before committing.", so fingerprinting the first line alone makes one
// record stand for every unrelated failure in the window.
func escapeFingerprint(stage string, o EscapeOptions) string {
	sum := sha256.Sum256([]byte(normalizeRepoSpelling(o.Repo) + "\n" + stage + "\n" + escapeDiagnostic(o)))
	return hex.EncodeToString(sum[:8])
}

// escapeDiagnostic is the part of an escape that identifies WHAT failed: the
// failing test names when the evidence names any, otherwise its first
// meaningful line, otherwise the reason.
func escapeDiagnostic(o EscapeOptions) string {
	if names := ExtractFailingTests(o.Evidence); len(names) > 0 {
		return strings.Join(names, ",")
	}
	if named := failingListLine(o.Evidence); named != "" {
		return named
	}
	if line := firstLine(strings.TrimSpace(o.Evidence)); line != "" {
		return line
	}
	return firstLine(strings.TrimSpace(o.Reason))
}

// failingListLine reads the `failing: a, b` line the mechanical rejection
// writes. It is the same set ExtractFailingTests would find in the raw output,
// but a rejection MESSAGE carries only a tail snippet of that output, so the
// names can survive there and nowhere else.
func failingListLine(evidence string) string {
	for line := range strings.SplitSeq(strings.ReplaceAll(evidence, "\r\n", "\n"), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "failing:"); ok {
			return strings.Join(strings.Fields(strings.ReplaceAll(rest, ",", " ")), ",")
		}
	}
	return ""
}

// recordEscapeOnce records unless an OPEN, ALREADY-OPENED record of the same
// class is younger than the window.
//
// Three rules, each one a hole somebody could otherwise fall through. A
// CLOSED record never suppresses: a hole that reopens after a fix is the
// loudest signal this loop produces. A record whose issue never opened does
// not suppress either — burning a class for a week because gh was briefly
// unreachable is the worst of both worlds — it is RETRIED, so the offline
// sighting gets its issue instead of a second record being appended. And a
// suppression says so on w: a recorder that goes quiet is indistinguishable
// from one that is broken.
func recordEscapeOnce(stage string, o EscapeOptions, w io.Writer) (EscapeRecord, bool) {
	o.Fingerprint = escapeFingerprint(stage, o)
	if seen, ok := recentEscape(o.Fingerprint); ok {
		if seen.Issue != "" {
			fmt.Fprintf(w, "gate escape: %s already open for this class (%s), recorded %s — not opening a second\n",
				seen.Issue, o.Fingerprint, seen.At.Format(time.RFC3339))
			return seen, false
		}
		// Recorded while gh was unreachable: give THAT record its issue
		// rather than appending a duplicate.
		if url, number, err := openEscapeIssue(o.Repo, seen); err == nil {
			seen.Issue, seen.Number = url, number
			updateEscape(seen)
		} else if !errors.Is(err, errNoIssueTarget) {
			fmt.Fprintf(w, "escape %s: %v\n", seen.ID, err)
		}
		return seen, true
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
// the local gate ran a suite on the commit it is failing — and without that,
// a CI red is just a CI red, not evidence about the gate. The fact travels as
// a GIT NOTE on the commit: `refs/notes/gate` holds `green <tree>` for a
// commit whose pre-commit gate actually ran a suite and it passed.
//
// A note rather than a commit trailer, because a commit message says what the
// change does and nothing else — the managed block promises that, and a repo
// that keeps its history undercover would be the first to notice a tool
// writing into it. A note is out-of-band, rewritable without touching
// history, and fetched only by whoever wants it.
//
// The note names the TREE it was written for. That is not tamper-proof — any
// note can be rewritten by whoever can write the ref — but it does not travel
// BETWEEN TREES BY ACCIDENT, which is what would otherwise happen the first
// time somebody rebased or cherry-picked a proven commit onto a different
// base. A note describing a tree the commit does not have is ignored.

// gateNotesRef is the notes ref, in its short `git notes --ref=` spelling.
const gateNotesRef = "gate"

// GateNotesRefFull is the fully-qualified ref, for a fetch or a push spec.
const GateNotesRefFull = "refs/notes/" + gateNotesRef

// gateGreenNote is the note body for a proven tree.
func gateGreenNote(tree string) string { return "green " + tree }

// greenSuiteStampFile remembers the tree a suite just went green on, one per
// repo. It exists only to survive the few milliseconds between the pre-commit
// hook and the post-commit hook, which are separate processes.
func greenSuiteStampFile(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	return gcStatePath("green-suite." + repoStateKey(repoRoot) + ".txt")
}

// suiteRanGreen records that a root group's suite actually RAN and passed
// during this gate process. A cache hit is deliberately not that: it says the
// identical tree was proven earlier, which is a fine reason to skip a rerun
// and a poor basis for a claim CI will weigh its own red against. It is also
// what makes an amend note-free — an amend re-runs the gate, hits the cache,
// and proves nothing new.
var suiteRanGreen atomic.Bool

// noteSuiteGreen is called by the suite stage on a real green.
func noteSuiteGreen() { suiteRanGreen.Store(true) }

// stampGreenSuite records the tree the passing suite ran against, so the
// post-commit hook can write the note for exactly that tree.
func stampGreenSuite(repoRoot string) {
	path := greenSuiteStampFile(repoRoot)
	tree := indexTree(repoRoot)
	if path == "" || tree == "" {
		return
	}
	_ = os.WriteFile(path, []byte(tree), 0o600)
}

// StampGreenSuiteIfProven is the hook-side spelling: the commit gate calls it
// after allowing a commit, and it stamps ONLY when a suite in this process
// actually ran green. A gate that allowed a commit because there was nothing
// to test has proven nothing and must not say it did.
func StampGreenSuiteIfProven(repoRoot string) {
	if !suiteRanGreen.Load() {
		return
	}
	stampGreenSuite(repoRoot)
}

// indexTree is the tree the staged index would commit as.
//
// It runs with GIT_INDEX_FILE HONOURED, unlike every other git call the gate
// makes. `git commit -a` and `git commit -- <paths>` build a TEMPORARY index
// and point the hooks at it through that variable; reading `.git/index`
// instead names a different tree, and does it while the commit holds
// `.git/index.lock`, so the call can fail outright. Either way the stamp
// never matches and no commit made that way is ever gated in CI's eyes.
//
// `git write-tree` WRITES tree objects into the object database, which is
// exactly what the commit is about to do anyway, so it adds no garbage a
// commit would not.
func indexTree(repoRoot string) string {
	cmd := exec.Command(gitBinary(), "write-tree")
	cmd.Dir = repoRoot
	env := cleanGitEnv()
	if idx := os.Getenv("GIT_INDEX_FILE"); idx != "" {
		env = append(env, "GIT_INDEX_FILE="+idx)
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PostCommit is the post-commit hook: it writes the gate note on the commit
// just made, when a suite went green for exactly that tree. The stamp is
// CONSUMED, so it can vouch for one commit and no other — an amend makes a
// new commit whose gate run only hit the cache, and gets no note, which is
// the right answer for a commit no suite has run against.
//
// Best-effort throughout: this is a channel, not a check, and the commit has
// already been made by the time it runs.
func PostCommit(repoRoot string) {
	path := greenSuiteStampFile(repoRoot)
	if path == "" {
		return
	}
	stamped, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = os.Remove(path)
	tree, ok := revTree(repoRoot, "HEAD")
	if !ok || tree == "" || strings.TrimSpace(string(stamped)) != tree {
		return
	}
	_, _ = git(repoRoot, "notes", "--ref="+gateNotesRef, "add", "-f", "-m", gateGreenNote(tree), "HEAD")
}

// commitCarriesGreenGate reports whether rev carries a gate note claiming a
// green suite for rev's OWN tree.
func commitCarriesGreenGate(repoRoot, rev string) bool {
	out, err := git(repoRoot, "notes", "--ref="+gateNotesRef, "show", rev)
	if err != nil {
		return false
	}
	tree, ok := revTree(repoRoot, rev)
	if !ok {
		return false
	}
	return strings.TrimSpace(out) == gateGreenNote(tree)
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

// isReceiptRejection reports whether the receipt stage produced the
// rejection. Every one of them carries receiptRejectionMarker — the sentence
// constant across every root, since the hint half now names a per-repo
// runner — which is what makes the family recognisable; a pinned test builds
// two of them and asserts this reads both.
func isReceiptRejection(message string) bool {
	// Two markers, because the family has two shapes. blockReceipt writes the
	// explaining sentence; blockMissingReceipt deliberately does not — it is
	// ONE line ending in the remedy, which is what a session at a blocked
	// merge needs. Reading only the first marker meant the most common merge
	// refusal there is fell through to the generic branch and was filed as an
	// escape against a pre-commit gate that has no receipt stage to miss,
	// which is the noise this exclusion exists to prevent.
	return strings.Contains(message, receiptRejectionMarker) ||
		strings.Contains(message, missingReceiptMarker)
}

// NoteMergeGateEscape records the merge gate's rejection as an escape when the
// rejection is evidence about the GATE rather than about the lane:
//
//   - unaccepted mutation survivors: a code path no test constrains reached
//     the last gate that could stop it, and no earlier stage judges survivors;
//   - a lane whose pre-commit gate ran a suite green on the SAME TREE the
//     merge gate is now refusing: two gates disagreed about one tree, so the
//     cheaper one is missing a stage.
//
// Every other rejection is the gate WORKING. The receipt family in particular
// is excluded wholesale apart from survivors: a lane with no receipt, or one
// measured against another base, is refused by a stage the pre-commit gate
// does not run at all, and it is the most common merge rejection there is —
// recording it would make the loop's loudest signal its noisiest.
//
// Best-effort and silent on failure: the merge has already been refused by
// the time this runs, and its verdict is not this function's to change.
func NoteMergeGateEscape(repoRoot, message string, w io.Writer) {
	stage, reason := "", ""
	switch {
	case isUnacceptedSurvivorRejection(message):
		stage = "merge:mutation-receipt"
		reason = "a lane reached the merge gate with unaccepted mutation survivors — a code path no test constrains"
	case isReceiptRejection(message):
		return
	case mergeTipCarriesGreenGate(repoRoot):
		stage = "merge:premergecommit"
		reason = "the merge gate refused a lane whose pre-commit gate had run a suite green on the same tree"
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

// mergeTipCarriesGreenGate reports whether the lane being merged carries a
// gate note for its OWN tree — which is the tree comparison this trigger
// turns on: the note names the tree it was written for, so a note that
// travelled from another commit says nothing about this one.
func mergeTipCarriesGreenGate(repoRoot string) bool {
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		return false
	}
	return commitCarriesGreenGate(repoRoot, tip.Rev)
}

// --- trigger (b): CI ---------------------------------------------------------

// CIEscapeOptions is what the CI job reported.
type CIEscapeOptions struct {
	Repo string
	// Job is the failing workflow job, and part of the fingerprint's stage.
	Job      string
	Reason   string
	Evidence string
	// Labels and Check ride through unchanged: a themed CI failure belongs in
	// the project's own filter, and the check that could have caught it is
	// the recorder's to name here as anywhere else.
	Labels []string
	Check  string
}

// RecordCIEscape is what the CI job runs when a workflow fails: it records an
// escape ONLY when the tip it failed on carries the gate note for its own
// tree. Without that, CI red says nothing about the gate — it says the gate
// never ran — and recording it would fill the loop with pushes nobody gated.
// The bool says whether anything was recorded.
func RecordCIEscape(o CIEscapeOptions, w io.Writer) (EscapeRecord, bool) {
	if !commitCarriesGreenGate(o.Repo, "HEAD") {
		fmt.Fprintf(w, "gate escape: HEAD carries no green gate note, so this CI failure is not evidence about the gate — nothing recorded\n")
		return EscapeRecord{}, false
	}
	reason := o.Reason
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("%s failed on a tip the local gate passed green", o.Job)
	}
	// The local dedupe store is this box's gate-state, and a CI runner is
	// routinely ephemeral: with an empty log every re-run would open another
	// issue for the same red. So GitHub is asked first — an OPEN issue
	// already carrying this fingerprint IS the record.
	stage := "ci:" + o.Job
	fp := escapeFingerprint(stage, EscapeOptions{Repo: o.Repo, Evidence: o.Evidence, Reason: reason})
	if number, found := openIssueWithFingerprint(o.Repo, fp); found {
		fmt.Fprintf(w, "gate escape: issue #%d already carries fingerprint %s — not opening a second\n", number, fp)
		return EscapeRecord{}, false
	}
	return recordEscapeOnce(stage, EscapeOptions{
		Reason:   reason,
		Kind:     EscapeKind,
		FromCI:   o.Job,
		Evidence: o.Evidence,
		Repo:     o.Repo,
		Labels:   o.Labels,
		Check:    o.Check,
	}, w)
}

// openIssueWithFingerprint finds an open escape issue whose body carries
// fingerprint. A gh that cannot answer finds none, which at worst opens one
// duplicate — never a lost signal.
func openIssueWithFingerprint(repo, fingerprint string) (int, bool) {
	if repo == "" || fingerprint == "" || !ghAvailable() || !hasGitHubRemote(repo) {
		return 0, false
	}
	out, err := runGh(repo, "issue", "list", "--label", EscapeKind, "--state", "open", "--limit", "200", "--json", "number,body")
	if err != nil {
		return 0, false
	}
	start, end := strings.Index(out, "["), strings.LastIndex(out, "]")
	if start < 0 || end < start {
		return 0, false
	}
	var docs []struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
	}
	if json.Unmarshal([]byte(out[start:end+1]), &docs) != nil {
		return 0, false
	}
	for _, d := range docs {
		if strings.Contains(d.Body, issueFingerprintKey+" "+fingerprint) {
			return d.Number, true
		}
	}
	return 0, false
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

// RecordOverrideCandidates records each candidate as a false positive and
// returns how many were newly recorded. Called by `escape sync`: an override
// is a prompt to look at a check, and the looking happens on somebody's own
// schedule, not in the middle of the session that was annoyed.
//
// It carries the same two guards the demote scan does, for the same reason.
// With no gh or no GitHub remote nothing can be opened, so reporting "opened
// N" from local records alone would be a number that means nothing. And a
// check re-flagged every week must not collect a new issue every week: the
// OPEN issue is the record, and the local fingerprint cannot see one opened
// from another checkout or on another box.
func RecordOverrideCandidates(repo string, candidates []OverrideCandidate, w io.Writer) int {
	if len(candidates) == 0 || repo == "" || !ghAvailable() || !hasGitHubRemote(repo) {
		return 0
	}
	open := openFalsePositiveTitles(repo)
	opened := 0
	for _, c := range candidates {
		if titleMentions(open, c.Stage) {
			continue
		}
		r, recorded := recordEscapeOnce(c.Stage, EscapeOptions{
			Reason:   c.Stage + " " + c.Reason,
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
