package tdd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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

// DemoteCandidates names every check whose count rose in BOTH of the last two
// weeks — two consecutive rises, so one busy week is not a signal. Sorted, so
// the report is stable.
func DemoteCandidates(r io.Reader, now time.Time) []string {
	// weeks[0] is the last 7 days, [1] the 7 before it, [2] the 7 before that.
	weeks := [3]map[string]int{{}, {}, {}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok {
			continue
		}
		check := demoteCheckName(e.verdict)
		if check == "" {
			continue
		}
		age := now.Sub(e.at)
		if age < 0 || age >= 3*demoteWeek {
			continue
		}
		weeks[int(age/demoteWeek)][check]++
	}

	var out []string
	for check, last := range weeks[0] {
		if last > weeks[1][check] && weeks[1][check] > weeks[2][check] {
			out = append(out, check)
		}
	}
	sort.Strings(out)
	return out
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
func RecordDemoteCandidates(repo string, candidates []string, w io.Writer) int {
	if len(candidates) == 0 || repo == "" || !ghAvailable() || !hasGitHubRemote(repo) {
		return 0
	}
	riseWindowStart := time.Now().UTC().Add(-2 * demoteWeek)
	opened := 0
	for _, check := range candidates {
		fp := demoteFingerprint(repo, check)
		if m, found := findIssueByFingerprint(repo, []string{FalsePositiveKind}, "all", fp); found {
			// An open issue is already the record. A closed one stands too,
			// unless it closed before the rise window even started — only
			// then did every event that flagged this run happen after the
			// human's close.
			if m.Open || m.ClosedAt.IsZero() || m.ClosedAt.After(riseWindowStart) {
				continue
			}
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
// per candidate.
func DemoteCandidateLines(candidates []string) string {
	var b strings.Builder
	for _, c := range candidates {
		fmt.Fprintf(&b, "demote-candidate: %s (refusals rose two weeks running — demote, narrow or fix it)\n", c)
	}
	return b.String()
}
