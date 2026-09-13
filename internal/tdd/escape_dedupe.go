package tdd

import (
	"encoding/json"
	"strings"
	"time"
)

// The dedupe that decides whether a sighting already has an issue.
//
// It asks GITHUB rather than this box, because the local record store is
// gate-state on one machine: another checkout, another box, or an ephemeral
// CI runner has none of it, and a guard that cannot see the issue it already
// opened opens a second one.
//
// The rule the whole file exists for is about a CLOSED issue. Matching only
// OPEN ones — which is what the escape half did — holds exactly while nobody
// has done anything about the miss, and stops the moment somebody does: the
// fix merges, the issue closes, and the next sync re-derives the same
// candidate from the same evidence and files it again. The rolling seven-day
// gate.log window guarantees that evidence is still there for a week after
// the fix that answered it, so the duplicate is not a race, it is certain.
//
// So a closed issue suppresses too, and what lifts the suppression is
// EVIDENCE NEWER THAN THE CLOSE. Pre-fix log lines are older than the close
// by definition, so they say nothing; a refusal that happens again afterwards
// is a fix that did not hold, and files its own issue. That second half is
// the one to protect: a suppression that never lifts loses the signal
// entirely, which is worse than the duplicate it was written to stop.

// fingerprintMatch is what findIssueByFingerprint reports about an issue it
// found: which one, whether it is still open, and — when it is not — when
// it closed. The closed timestamp is what lets a caller judge whether new
// evidence postdates a human's decision instead of just re-litigating it.
type fingerprintMatch struct {
	Number   int
	Open     bool
	ClosedAt time.Time
}

// findIssueByFingerprint finds an issue whose body carries fingerprint,
// among labels (queried one at a time — gh reads repeated --label flags as
// an AND, so a query across several would silently ask for issues carrying
// ALL of them) and states ("open", "closed" or "all", gh's own spelling). A
// gh that cannot answer finds none, which at worst opens one duplicate —
// never a lost signal.
func findIssueByFingerprint(repo string, labels []string, state, fingerprint string) (fingerprintMatch, bool) {
	if repo == "" || fingerprint == "" || !ghAvailable() || !hasGitHubRemote(repo) {
		return fingerprintMatch{}, false
	}
	for _, label := range labels {
		out, err := runGh(repo, "issue", "list", "--label", label, "--state", state, "--limit", "200", "--json", "number,body,state,closedAt")
		if err != nil {
			continue
		}
		start, end := strings.Index(out, "["), strings.LastIndex(out, "]")
		if start < 0 || end < start {
			continue
		}
		var docs []struct {
			Number   int    `json:"number"`
			Body     string `json:"body"`
			State    string `json:"state"`
			ClosedAt string `json:"closedAt"`
		}
		if json.Unmarshal([]byte(out[start:end+1]), &docs) != nil {
			continue
		}
		for _, d := range docs {
			if !strings.Contains(d.Body, issueFingerprintKey+" "+fingerprint) {
				continue
			}
			m := fingerprintMatch{Number: d.Number, Open: strings.EqualFold(d.State, "OPEN")}
			if t, err := time.Parse(time.RFC3339, d.ClosedAt); err == nil {
				m.ClosedAt = t
			}
			return m, true
		}
	}
	return fingerprintMatch{}, false
}

// issueAlreadyAnswers reports whether an issue carrying fingerprint already
// stands for a sighting whose newest evidence is dated evidenceAt, and which
// issue that is. Both halves of the loop — the escape recorder and the
// false-positive recorder — go through it, so the two cannot drift apart
// again: they differ only in the label they file under and in what they can
// say about when their evidence happened.
//
// An OPEN issue is the record, full stop. A CLOSED one stands unless
// evidenceAt is strictly after its close. A zero evidenceAt is a caller that
// cannot date its evidence, and therefore has nothing newer than the close to
// offer: it stays suppressed. So is a close GitHub could not put a date on,
// for the same reason — an undated judgement is still a judgement, and
// treating it as liftable would reopen every one of them on the next sync.
func issueAlreadyAnswers(repo string, labels []string, fingerprint string, evidenceAt time.Time) (int, bool) {
	m, found := findIssueByFingerprint(repo, labels, "all", fingerprint)
	if !found {
		return 0, false
	}
	if m.Open {
		return m.Number, true
	}
	if m.ClosedAt.IsZero() || !evidenceAt.After(m.ClosedAt) {
		return m.Number, true
	}
	return m.Number, false
}

// overrideEscapeOptions is the false positive an override candidate is filed
// as. It is built here, once, because the fingerprint has to be computed
// BEFORE the record is written — the guard that decides whether to write it
// is keyed on that fingerprint — and a second spelling of these fields would
// compute a fingerprint that matches no issue ever opened.
func overrideEscapeOptions(repo string, c OverrideCandidate) EscapeOptions {
	return EscapeOptions{
		Reason:   c.Stage + " " + c.Reason,
		Kind:     FalsePositiveKind,
		Evidence: c.Evidence,
		Repo:     repo,
		Check:    strings.TrimPrefix(c.Stage, "override:"),
	}
}

// overrideFingerprint is the fingerprint the issue for this candidate carries.
func overrideFingerprint(repo string, c OverrideCandidate) string {
	return escapeFingerprint(c.Stage, overrideEscapeOptions(repo, c))
}
