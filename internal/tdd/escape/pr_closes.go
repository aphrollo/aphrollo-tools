package escape

// pr_closes_test.go carries the mutation-proof coverage for this file.

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// A PR body that NAMES an issue and does not actually close it is invisible
// at merge time: a PR with a closing keyword and a PR without one read
// identically, and the difference only surfaces when someone audits the
// issue list by hand (issue #463). Two shapes are worth catching mechanically:
//
//   - a bare "#123" mention with no closing keyword — sometimes a genuine
//     cross-reference ("related to #123"), so this is a WARNING naming the
//     number, never a refusal.
//   - "closes #A, #B" — GitHub honours a keyword only for the number
//     directly after it, so every number past the first is silently
//     abandoned. This has one correct rewrite (`closes #A, closes #B`) and no
//     legitimate use, so it is an ERROR.

// issueMentionRe finds every "#NNN" in a PR body, regardless of what (if
// anything) precedes it.
var issueMentionRe = regexp.MustCompile(`#(\d+)`)

// closingKeywordCommaListRe finds one closing keyword followed by two or more
// comma-joined issue numbers — the shape GitHub closes only the first of.
var closingKeywordCommaListRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fixe?[sd]?|resolve[sd]?)\s+#\d+(?:\s*,\s*#\d+)+`)

// closingKeywordSingleRe finds one closing keyword directly naming one issue
// number — the correct, unambiguous shape.
var closingKeywordSingleRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fixe?[sd]?|resolve[sd]?)\s+#\d+`)

// closingKeywordFindings classifies every issue mention in a PR body. Warnings
// are bare mentions worth a human's eye; errors are comma lists GitHub cannot
// honour past the first number. A mention consumed by an earlier pass is
// masked out of the body before the next regex runs, so "Closes #A, #B" is
// counted once (as the comma-list error) rather than also flagged as a bare
// mention of #B.
func closingKeywordFindings(body string) (warnings, errors []string) {
	work := []byte(body)

	for _, span := range closingKeywordCommaListRe.FindAllStringIndex(body, -1) {
		text := body[span[0]:span[1]]
		var numbers []string
		for _, m := range issueMentionRe.FindAllStringSubmatch(text, -1) {
			numbers = append(numbers, "#"+m[1])
		}
		rewrite := make([]string, len(numbers))
		for i, n := range numbers {
			rewrite[i] = "closes " + n
		}
		errors = append(errors, fmt.Sprintf("%q closes only %s — GitHub needs one keyword per number; rewrite as %q",
			strings.TrimSpace(text), numbers[0], strings.Join(rewrite, ", ")))
		blankSpan(work, span[0], span[1])
	}

	for _, span := range closingKeywordSingleRe.FindAllIndex(work, -1) {
		blankSpan(work, span[0], span[1])
	}

	seen := map[string]bool{}
	for _, m := range issueMentionRe.FindAllStringSubmatch(string(work), -1) {
		number := "#" + m[1]
		if !seen[number] {
			seen[number] = true
			warnings = append(warnings, number)
		}
	}
	return warnings, errors
}

// blankSpan blanks out work[start:end] with spaces, preserving length and
// byte offsets so a later regex pass never sees the masked text but
// positions found in earlier passes stay valid.
func blankSpan(work []byte, start, end int) {
	for i := start; i < end; i++ {
		work[i] = ' '
	}
}

// CheckPRCloses reads a PR's body through gh and reports its closing-keyword
// findings, printing one line per warning or error. It returns false only on
// an error finding (a comma list) — a bare mention is worth a human's
// attention but never fails the check, since a legitimate cross-reference
// ("related to #123") looks identical to a forgotten keyword.
func CheckPRCloses(repo, pr string, w io.Writer) (bool, error) {
	if !ghAvailable() {
		return false, fmt.Errorf("check-closes needs the GitHub CLI (gh) on PATH")
	}
	body, err := prBody(repo, pr)
	if err != nil {
		return false, err
	}
	warnings, errs := closingKeywordFindings(body)
	for _, wnt := range warnings {
		fmt.Fprintf(w, "warning: %s mentioned with no closing keyword (fine if it is a reference, not a fix)\n", wnt)
	}
	for _, e := range errs {
		fmt.Fprintf(w, "error: %s\n", e)
	}
	if len(warnings) == 0 && len(errs) == 0 {
		fmt.Fprintf(w, "PR #%s: no closing-keyword issues\n", pr)
	}
	return len(errs) == 0, nil
}

// prBody reads one PR's body through gh.
func prBody(repo, pr string) (string, error) {
	out, err := runGh(repo, "pr", "view", pr, "--json", "body")
	if err != nil {
		return "", err
	}
	var doc struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &doc); err != nil {
		return "", fmt.Errorf("reading PR #%s: %w", pr, err)
	}
	return doc.Body, nil
}
