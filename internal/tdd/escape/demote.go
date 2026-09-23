package escape

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// A check that refuses more work every week is doing one of two things:
// catching a real regression in how people write code, or refusing work that
// was correct. The second is far more common, and it is invisible from
// inside — each denial looks reasonable, and the trend is only in the log.
//
// So the log is read for it. A check whose refusals-plus-waivers rose in each
// of the last two weeks is named as a DEMOTE CANDIDATE, and (where gh can
// reach the remote) recorded as a false positive in the same escape loop, one
// issue per check — open or closed. That is a prompt to look, not a verdict:
// the answer may be to fix the check, to narrow it, or to demote it from
// deny to warn, and a human closing that issue is a judgement the next sync
// must not undo on the evidence that judgement already saw.

// demoteWeek is the window the trend is measured in.
const demoteWeek = 7 * 24 * time.Hour

// demoteCheckPrefixes are the verdicts that name a CHECK refusing or waived.
// An override is a session turning the whole gate off, not a check refusing
// anything, so it is deliberately not here.
var demoteCheckPrefixes = []string{"pretooluse-denied:", "smell-escape:"}

// DemoteCandidate is one check trending up in ONE repo: a law belongs to the
// tree that declares it, so both halves are needed to say whose rule this is
// and whose tracker the question belongs on.
type DemoteCandidate struct {
	Repo  string
	Check string
}

// DemoteCandidates names every check whose refusals PER ACTIVE LANE rose in
// BOTH of the last two weeks, within one repo — see demote_trend.go for why
// each of those three words is load-bearing.
func DemoteCandidates(r io.Reader, now time.Time) []DemoteCandidate {
	return readDemoteTrend(r, now).candidates(now)
}

// demoteCheckName is the check a verdict belongs to, "" for a verdict that
// names no check.
func demoteCheckName(verdict string) string {
	for _, p := range demoteCheckPrefixes {
		if name, ok := strings.CutPrefix(verdict, p); ok {
			return name
		}
	}
	return ""
}

// demoteFingerprint identifies a CHECK, not a sighting of it: no timestamp,
// so the same check computes the same fingerprint on every sync. That is
// what lets the issue that already exists for it — open OR closed — be
// found by asking GitHub what this box does not remember, rather than by
// guessing from words in a title.
func demoteFingerprint(repo, check string) string {
	return escapeFingerprint("demote:"+check, EscapeOptions{Repo: repo, Reason: check})
}

// RecordDemoteCandidates opens one false-positive issue per candidate that
// does not already have one, and returns how many it opened.
//
// A check re-flagged every week must not collect a new issue every week: an
// OPEN issue carrying its fingerprint IS the record. A CLOSED one is a
// human's judgement, and the next sync must not undo it on the same
// evidence that judgement already saw — so a closed issue also stands,
// UNLESS the two-week rise that flagged this run happened entirely AFTER
// the close, which is the only way the evidence is genuinely new rather
// than a re-litigation.
//
// A candidate whose refusals came from ANOTHER checkout is not this repo's
// question: the law that refused them lives in that tree, is very often a
// rule this one does not carry at all, and an issue opened here asks an owner
// to judge a rule they cannot read. It is dropped rather than filed anywhere,
// because the repo that owns it is the only place it could honestly go.
func RecordDemoteCandidates(repo string, candidates []DemoteCandidate, w io.Writer) int {
	if len(candidates) == 0 || repo == "" || !ghAvailable() || !hasGitHubRemote(repo) {
		return 0
	}
	riseWindowStart := time.Now().UTC().Add(-2 * demoteWeek)
	opened := 0
	for _, candidate := range candidates {
		if !demoteSameRepo(repo, candidate.Repo) {
			continue
		}
		check := candidate.Check
		fp := demoteFingerprint(repo, check)
		// The same rule the escape and override halves use, through the same
		// guard so the three cannot drift apart again. What this half can say
		// about WHEN its evidence happened is the start of the two-week rise
		// window: a close inside that window saw some of the events that
		// flagged this run, so the judgement stands; a close before it saw
		// none of them, and the trend is genuinely new.
		if _, answered := issueAlreadyAnswers(repo, []string{FalsePositiveKind}, fp, riseWindowStart); answered {
			continue
		}
		r, err := RecordEscape(EscapeOptions{
			Reason: fmt.Sprintf("%s denies more every week — demote, narrow or fix it", check),
			Kind:   FalsePositiveKind,
			Repo:   repo,
			Evidence: fmt.Sprintf("gate.log: %s refusals rose in each of the last two weeks. "+
				"Judge whether the rule is right and the code drifted, or the rule is refusing correct work.", check),
			Fingerprint: fp,
		}, w)
		if err != nil {
			fmt.Fprintf(w, "demote-candidate %s: %v\n", check, err)
			continue
		}
		opened++
		fmt.Fprintf(w, "demote-candidate %s recorded as %s\n", check, r.ID)
	}
	return opened
}

// openFalsePositiveTitles reads the titles of the open false-positive issues.
// A gh that cannot answer yields none, which at worst opens one duplicate —
// never a lost signal.
func openFalsePositiveTitles(repo string) []string {
	out, err := runGh(repo, "issue", "list", "--label", FalsePositiveKind, "--state", "open", "--json", "title")
	if err != nil {
		return nil
	}
	var docs []struct {
		Title string `json:"title"`
	}
	start, end := strings.Index(out, "["), strings.LastIndex(out, "]")
	if start < 0 || end < start {
		return nil
	}
	if json.Unmarshal([]byte(out[start:end+1]), &docs) != nil {
		return nil
	}
	titles := make([]string, 0, len(docs))
	for _, d := range docs {
		titles = append(titles, d.Title)
	}
	return titles
}

func titleMentions(titles []string, check string) bool {
	for _, t := range titles {
		if strings.Contains(t, check) {
			return true
		}
	}
	return false
}

// DemoteCandidateLines is what `gate stats` prints about the trend, one line
// per candidate. It names the repo as well as the check: the same name is a
// different law in each tree, and the reader has to know which one to go and
// look at.
func DemoteCandidateLines(candidates []DemoteCandidate) string {
	var b strings.Builder
	for _, c := range candidates {
		fmt.Fprintf(&b, "demote-candidate: %s in %s (refusals per active lane rose two weeks running — demote, narrow or fix it)\n",
			c.Check, c.Repo)
	}
	return b.String()
}
