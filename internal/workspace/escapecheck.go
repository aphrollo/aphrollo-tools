package workspace

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A PR closing an escape or false-positive issue without actually changing a
// check, or whose body closes more than one issue behind a single keyword
// GitHub only honours the first of, used to be caught by CI's escape-closure
// and pr-closes-check jobs -- after the PR already existed. Both run locally
// instead: before the PR opens (closureChecksBeforePR, called from pr/submit/
// ship right beside mutantsBeforePR), and again at `workspace merge` for a PR
// this tool did not itself open (escapeClosureBeforeMerge).

// closureChecksBeforePR resolves the PR's title/body (filling from commits
// exactly as ghCreatePR would, so the check judges the text that is about to
// ship) and refuses to open the PR when its body cannot honour every closing
// keyword it carries, or when an escape/false-positive issue it names is not
// actually closed by this branch's diff. It returns the resolved title/body
// so the caller passes them straight to ghCreatePR without a second fill.
//
// A branch with no local merge base against origin/base (a brand-new repo, a
// base ref this checkout has not fetched) cannot be diffed locally at all;
// that case is judged again at `workspace merge` instead of blocking the PR
// on infrastructure the branch itself did not cause.
func closureChecksBeforePR(wt, base, branch, title, body string, w io.Writer) (string, string, error) {
	if title == "" {
		title, body = fillTitleBody(wt, base, branch)
	}
	if !checkBodyCloses(body, w) {
		return title, body, errors.New("PR not opened: its body closes more than one issue behind a single keyword — GitHub only honours the first (see above)")
	}
	mergeBase := laneMergeBase(wt, "origin/"+base)
	if mergeBase == "" {
		fmt.Fprintf(w, "escape-closure: no merge base with origin/%s — not checked locally, judged again at merge\n", base)
		return title, body, nil
	}
	texts := append([]string{body}, commitMessagesSince(wt, mergeBase, "HEAD")...)
	ok, err := verifyClosureLocal(wt, texts, mergeBase, "HEAD", w)
	if err != nil {
		return title, body, fmt.Errorf("escape closure check: %w", err)
	}
	if !ok {
		return title, body, errors.New("PR not opened: an escape or false-positive issue this branch claims to close needs a law, gate stage, or named check changed (see above)")
	}
	return title, body, nil
}

// checkBodyCloses and verifyClosureLocal are the seams over the escape
// package's pre-PR checks — package vars so pr/submit/ship tests drive
// closureChecksBeforePR's OWN wiring (which text and which revisions it
// hands over) without gh or the network, mirroring escapeClosureBeforeMerge
// beside them. checkBodyCloses needs neither and is safe to run for real in
// every test; verifyClosureLocal calls gh the moment a body or commit
// message actually closes an issue.
var checkBodyCloses = tdd.CheckBodyCloses
var verifyClosureLocal = tdd.VerifyClosureLocal

// commitMessagesSince is every commit's subject+body between base and head in
// wt, oldest first — the same texts GitHub reads a closing keyword out of
// inside a PR's own commits (readPRMeta, internal/tdd/escape/escape_verify.go).
func commitMessagesSince(wt, base, head string) []string {
	out, err := exec.Command("git", "-C", wt, "log", "--reverse", "--format=%H", base+".."+head).Output() // stderr-ok: a failed log here just yields no extra text, same as an empty range
	if err != nil {
		return nil
	}
	var texts []string
	for _, sha := range strings.Fields(string(out)) {
		subj, _ := exec.Command("git", "-C", wt, "log", "-1", "--format=%s", sha).Output() // stderr-ok: a failed subject read just omits it
		bod, _ := exec.Command("git", "-C", wt, "log", "-1", "--format=%b", sha).Output()  // stderr-ok: same as above
		texts = append(texts, strings.TrimSpace(string(subj)), strings.TrimSpace(string(bod)))
	}
	return texts
}

// escapeClosureBeforeMerge is the seam over the merge-time escape-closure
// checks (check-closes format + verify-closure content) for a PR this tool
// did not itself open — `gh pr create` run by hand never passes through
// closureChecksBeforePR above, so the same judgment runs here instead, once,
// right before the merge. A package var so merge tests drive Apply without gh
// or the network, mirroring the ghCIStatus seam beside it.
var escapeClosureBeforeMerge = func(wt string, prNumber int, w io.Writer) error {
	pr := strconv.Itoa(prNumber)
	if ok, err := tdd.CheckPRCloses(wt, pr, w); err != nil {
		return fmt.Errorf("check-closes: %w", err)
	} else if !ok {
		return errors.New("the PR body closes more than one issue behind a single keyword — GitHub only honours the first (see above)")
	}
	if ok, err := tdd.VerifyClosure(wt, pr, w); err != nil {
		return fmt.Errorf("verify-closure: %w", err)
	} else if !ok {
		return errors.New("an escape or false-positive issue this PR claims to close needs a law, gate stage, or named check changed (see above)")
	}
	return nil
}

// recordMergeCIEscape is the seam over recording an escape when CI is red on
// a tip the local gate already proved green — the merge verb's own
// replacement for CI's escape-record job, which needed a GitHub Actions run
// to see both halves of that disagreement. `workspace merge` already reads
// both itself: the gate note written at commit time (RecordCIEscape's own
// guard) and the CI state it just asked gh for.
var recordMergeCIEscape = tdd.RecordCIEscape
