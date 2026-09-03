package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// The open points of every consuming repo are GitHub issues now. That is
// strictly better than a markdown list — until you notice that a session
// never opens a browser, so the number is invisible to the one reader who
// could act on it. The weekly digest is the wrong shape for this: a count
// that changed since yesterday is worth seeing today.
//
// So one line at EVERY session start, and the cost is paid by a cache rather
// than by the session: one gh call per repo per hour, and a failure prints
// nothing at all. A line about GitHub being unreachable is not information a
// session can use, and printing it every prompt would train the reader to
// skip the line that carries the number.

// issuesCacheTTL is how long one fetch answers for. An hour: long enough that
// a day of sessions costs a handful of calls, short enough that a morning's
// triage shows up the same morning.
const issuesCacheTTL = time.Hour

// issuesFetchTimeout bounds the one gh call. A session start that waits on
// GitHub is a session start that hangs, and the line is a nicety: five
// seconds is generous for a call measured at ~2 s over 227 issues, and a
// remote slower than that reads as a failed fetch — silent, cached, one log
// line. A var so a test can shrink it.
var issuesFetchTimeout = 5 * time.Second

// issuesFetchFailedVerdict is the gate.log entry a failed fetch leaves. It is
// written once per window, not once per prompt, because the cache records the
// failure too.
const issuesFetchFailedVerdict = "issues-fetch-failed"

// issuesCacheFile is the per-repo cache, keyed the same way every other
// per-repo state file is.
func issuesCacheFile(repo string) string {
	return gcStatePath("issues." + repoStateKey(repo) + ".json")
}

// issuesCache is one remembered answer. Line is "" for a fetch that failed —
// remembering the failure is what keeps the log line and the gh process from
// repeating at every prompt.
type issuesCache struct {
	Schema int       `json:"schema"`
	At     time.Time `json:"at"`
	Line   string    `json:"line"`
}

// issueSummaryLine is the session-start line, "" when there is nothing to say
// (no repo, no gh, no GitHub remote, or a fetch that failed). now is passed in
// so the cache window is a decision the caller can test rather than a
// stopwatch reading.
func issueSummaryLine(repo string, now time.Time) string {
	if repo == "" {
		return ""
	}
	path := issuesCacheFile(repo)
	if path == "" {
		return ""
	}
	// A cache stamped in the FUTURE (a clock that moved) is treated as stale
	// rather than served until it expires.
	if c, ok := readIssuesCache(path); ok {
		if age := now.Sub(c.At); age >= 0 && age < issuesCacheTTL {
			return c.Line
		}
	}
	line, ok := fetchIssueSummary(repo, now)
	if !ok {
		appendGateLog("sessionstart", logToken(repo), "gh issue list", issuesFetchFailedVerdict, 0)
	}
	writeIssuesCache(path, issuesCache{Schema: StateSchema, At: now, Line: line})
	return line
}

func readIssuesCache(path string) (issuesCache, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return issuesCache{}, false
	}
	var c issuesCache
	if json.Unmarshal(data, &c) != nil || c.Schema != StateSchema {
		return issuesCache{}, false
	}
	return c, true
}

func writeIssuesCache(path string, c issuesCache) {
	if data, err := json.Marshal(c); err == nil {
		_ = writeFileAtomic(path, data)
	}
}

// fetchIssueSummary asks gh for the open issues and renders the line. The
// second result is false for a fetch that could not happen or failed — which
// is NOT the same as a repo with zero open issues, and the two must not
// collapse into one blank line.
func fetchIssueSummary(repo string, now time.Time) (string, bool) {
	if !ghAvailable() || !hasGitHubRemote(repo) {
		return "", false
	}
	// --limit is the whole page: gh defaults to 30, which would silently
	// under-count every repo that migrated a backlog.
	out, err := runGhTimeout(repo, issuesFetchTimeout, "issue", "list", "--state", "open", "--limit", "1000", "--json", "labels")
	if err != nil {
		return "", false
	}
	total, byLabel, ok := parseIssueLabels(out)
	if !ok {
		return "", false
	}
	// The escape count comes from the LOCAL record rather than a second gh
	// call: it is the number this tool owns, it is free, and it is the one
	// `gate stats` prints, so the two can never disagree.
	open, _ := OpenEscapes()
	return renderIssueSummary(total, byLabel, IssueLabels(repo), open), true
}

// parseIssueLabels counts the open issues and their labels. A payload it
// cannot read is a failed fetch, not an empty repo.
func parseIssueLabels(out string) (total int, byLabel map[string]int, ok bool) {
	start, end := strings.Index(out, "["), strings.LastIndex(out, "]")
	if start < 0 || end < start {
		return 0, nil, false
	}
	var docs []struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if json.Unmarshal([]byte(out[start:end+1]), &docs) != nil {
		return 0, nil, false
	}
	byLabel = map[string]int{}
	for _, d := range docs {
		for _, l := range d.Labels {
			byLabel[l.Name]++
		}
	}
	return len(docs), byLabel, true
}

// issueSummaryLabels is how many themes the line names before it stops being
// one line.
const issueSummaryLabels = 8

// renderIssueSummary draws the line. Themes come in the repo's DECLARED order
// when it declares one — that order is the project's own, and a count sorted
// by size reshuffles the line every day for no reason.
func renderIssueSummary(total int, byLabel map[string]int, declared []string, escapes int) string {
	var parts []string
	seen := map[string]bool{}
	for _, name := range declared {
		if n := byLabel[name]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", name, n))
			seen[name] = true
		}
	}
	var rest []string
	for name, n := range byLabel {
		if !seen[name] && n > 0 {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		parts = append(parts, fmt.Sprintf("%s:%d", name, byLabel[name]))
	}
	if len(parts) > issueSummaryLabels {
		parts = append(parts[:issueSummaryLabels], "…")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "gate: %d open issue%s", total, plural(total))
	if len(parts) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, " "))
	}
	fmt.Fprintf(&b, ", %d open escape%s — aphrollo gate issue / gate escape record", escapes, plural(escapes))
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
