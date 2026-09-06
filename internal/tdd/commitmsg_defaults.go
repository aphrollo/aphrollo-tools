package tdd

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// The undercover check in commitmsg.go answers "does this message describe
// HOW it was written"; the checks here answer the plainer question every
// repo asks regardless of that flag: does the subject say WHAT changed, and
// does a diff big enough to need one carry an explanation (issue #329). On
// for every repo the hook is installed in — nothing here can leak anything,
// so there is no reason to gate it behind an opt-in the way undercover is.

// vagueSubject is a subject that opens with a word carrying no information
// about what changed, followed by at most a few more characters: "fix",
// "wip", "cleanup src" and their plurals — never "Fix the race in the file
// watcher's debounce", the same open word but the rest of the line says
// what changed.
var vagueSubject = regexp.MustCompile(`(?i)^(fix|fixes|update|updates|wip|cleanup|minor|changes|misc|tweak|refactor|stuff|test|tests)\b.{0,12}$`)

// pathToken is a word that names a file rather than describes a change: it
// carries a path separator or ends in one of this repo's own source
// extensions.
var pathToken = regexp.MustCompile(`(?i)(/|\.(go|rs|md)$)`)

// mergeSubject is git's own auto-generated merge subject ("Merge branch
// 'lane/x'", "Merge pull request #4 from …"): nobody composes this line by
// hand, so the subject-shape rules below — which judge what a human chose to
// write — do not apply to it. The tell-detection layer still reads a merge
// commit's full message, unaffected by this exemption.
var mergeSubject = regexp.MustCompile(`^Merge (branch|tag|remote-tracking branch|pull request) `)

// defaultCommitMsgCheck runs the default (undercover-independent) checks
// against a message already read from disk, returning the blocking result
// if one of them fires. ws is the workspace root a repo's own
// commit-message-allow list is read from — the same manifest
// commit-message-deny already reads.
func defaultCommitMsgCheck(repoRoot, ws, body string) (GateResult, bool) {
	var none GateResult
	lines := strings.Split(body, "\n")
	subject := ""
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		subject = strings.TrimSpace(line)
		break
	}
	if subject == "" {
		return none, false
	}
	for _, raw := range cargoAphrolloPackages(ws, "commit-message-allow") {
		re, err := regexp.Compile(raw)
		if err == nil && re.MatchString(subject) {
			// A repo-declared shape (a release bump, say) is exempt from
			// every default check below — an unparseable pattern is skipped
			// the same way commit-message-deny already skips one, rather
			// than blocking every commit over a typo.
			return none, false
		}
	}

	if !mergeSubject.MatchString(subject) {
		if rule, msg := subjectShapeIssue(subject); rule != "" {
			return denyDefault(repoRoot, rule, msg), true
		}
	}

	if changed := stagedChangedLines(repoRoot); changed > 50 && !bodyExplains(lines[1:]) {
		return denyDefault(repoRoot, "body-required", fmt.Sprintf(
			"gate commit-msg: %d changed line(s) staged and the body never says why — add a sentence explaining the change, not just the files it touched",
			changed)), true
	}
	return none, false
}

// subjectShapeIssue answers the two shape rules #329 names as one deny list:
// a subject too vague or too short to say what changed, or a subject that is
// really just a list of the files the commit touched.
func subjectShapeIssue(subject string) (rule, message string) {
	if vagueSubject.MatchString(subject) {
		return "subject-deny", fmt.Sprintf(
			"gate commit-msg: the subject %q says how much changed, not what — name the defect or the feature instead", subject)
	}
	words := strings.Fields(subject)
	if len(words) < 4 {
		return "subject-deny", fmt.Sprintf(
			"gate commit-msg: the subject %q is under four words — say what changed, not just that something did", subject)
	}
	if isPathListSubject(words) {
		return "subject-path-list", fmt.Sprintf(
			"gate commit-msg: the subject %q names files, not the change they carry", subject)
	}
	return "", ""
}

// isPathListSubject reports whether a subject is, in substance, a list of
// touched files rather than a sentence: at least two tokens name a path, and
// every remaining token is punctuation or a connector — never a verb. One
// real word anywhere in the subject means it is prose, not a file list.
func isPathListSubject(words []string) bool {
	pathCount := 0
	for _, w := range words {
		w = strings.Trim(w, ",;:")
		if w == "" || strings.EqualFold(w, "and") || w == "&" {
			continue
		}
		if pathToken.MatchString(w) {
			pathCount++
			continue
		}
		return false
	}
	return pathCount >= 2
}

// bodyExplains reports whether the message body carries at least one line
// that reads as prose rather than a bare file name: four or more words, and
// not itself a path list.
func bodyExplains(bodyLines []string) bool {
	for _, line := range bodyLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		words := strings.Fields(trimmed)
		if len(words) >= 4 && !isPathListSubject(words) {
			return true
		}
	}
	return false
}

// shortstatNumber pulls every insertion/deletion count out of a `git diff
// --shortstat` line, e.g. " 3 files changed, 52 insertions(+), 4
// deletions(-)".
var shortstatNumber = regexp.MustCompile(`(\d+) insertions?\(\+\)|(\d+) deletions?\(-\)`)

// stagedChangedLines answers how many lines this commit's staged diff adds
// or removes, from `git diff --cached --shortstat` — the same count `git
// commit` itself reports. A git failure (a hook run outside a work tree, no
// git on PATH) answers zero rather than diagnosing itself: this gate is not
// the place to explain a broken git, and demanding a body over a mystery it
// cannot name would be worse than staying silent.
func stagedChangedLines(repoRoot string) int {
	out, err := exec.Command(gitBinary(), "-C", repoRoot, "diff", "--cached", "--shortstat").Output() // stderr-ok: failure silently answers zero, never surfaced
	if err != nil {
		return 0
	}
	total := 0
	for _, m := range shortstatNumber.FindAllStringSubmatch(string(out), -1) {
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			n, convErr := strconv.Atoi(g)
			if convErr == nil {
				total += n
			}
		}
	}
	return total
}

// denyDefault builds the blocking result for a default-layer rejection and
// logs it the same way the undercover layer does, under its own rule name so
// `gate stats` can tell the two families of rejection apart.
func denyDefault(repoRoot, rule, message string) GateResult {
	appendGateLog("commitmsg", logToken(repoRoot), "commit-msg", "commitmsg-rejected:"+rule, 0)
	return GateResult{Blocked: true, Message: message + "\nRewrite the message, then commit again."}
}
