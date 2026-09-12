package tdd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// An ESCAPE is a red that arrived after a local green: CI failed on a commit
// the gate passed, a merge gate refused what precommit allowed, a mutant
// survived, a playtest found a defect some check could have seen. Each one is
// the only direct evidence the gate has about what it is missing, and each
// one is normally lost — noticed in a terminal, fixed, forgotten, and the
// same class escapes again a month later.
//
// So an escape is RECORDED (a line in escapes.jsonl) and, when there is a
// GitHub remote and a `gh` to reach it, opened as a labelled issue with a
// fixed body. The body's third line is the whole point: an escape is closed
// by a LAW or a STAGE named in the fix, never by a sentence in a document.
// `verify-closure` is what enforces that at the PR, and the count is printed
// weekly at session start so it stays a number somebody owns.
//
// A false positive is the same loop pointing the other way: a check that
// refuses correct work is debt too, and the record is what turns "this rule
// is annoying" into a demotion somebody can defend.
//
// escapes.jsonl carries no schema field of its own: every reader here decodes
// a line straight into EscapeRecord with the stock JSON decoder, which drops
// an unrecognised key rather than failing on it — so a field a newer binary
// adds is read safely, if silently, by an older one with no version check
// needed on either side. See issue #511.

const (
	// EscapeKind is a red the gate should have caught and did not.
	EscapeKind = "escape"
	// FalsePositiveKind is a check that refused work which was correct.
	FalsePositiveKind = "false-positive"
)

// EscapeRecord is one entry in escapes.jsonl.
type EscapeRecord struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Reason   string    `json:"reason"`
	FromCI   string    `json:"from_ci,omitempty"`
	Evidence string    `json:"evidence,omitempty"`
	At       time.Time `json:"at"`
	Issue    string    `json:"issue,omitempty"`
	Number   int       `json:"number,omitempty"`
	Closed   bool      `json:"closed,omitempty"`
	// Labels are the project's own theme labels the issue also carries, so a
	// playtest defect lands in the theme filter as well as the escape one.
	Labels []string `json:"labels,omitempty"`
	// Check names the stage or law that could have caught this. It is what
	// separates an escape from a plain defect: a defect no check could have
	// seen is not evidence about the gate, and belongs in `gate issue`.
	Check string `json:"check,omitempty"`
	// ClosesBy names the law, stage or test file whose change would close
	// this escape. It fills the issue's closes-by line at record time, so the
	// fix is judged against a declaration somebody made when the evidence was
	// fresh rather than against an unfilled placeholder.
	ClosesBy string `json:"closes_by,omitempty"`
	// Fingerprint is stage + first diagnostic line, hashed. An automatic
	// recorder dedupes on it, so one recurring failure is one issue rather
	// than one per run.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// EscapeOptions is what `gate escape record` was told.
type EscapeOptions struct {
	Reason   string
	Kind     string
	FromCI   string
	Evidence string
	// Repo is the checkout the issue would be opened against; empty means
	// record locally and stop.
	Repo string
	// Labels, Check, ClosesBy and Fingerprint carry into the record — see
	// EscapeRecord.
	Labels      []string
	Check       string
	ClosesBy    string
	Fingerprint string
}

// EscapeLogPath is where the records live, "" when there is no state dir.
func EscapeLogPath() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "escapes.jsonl")
}

// RecordEscape writes one record and, when it can reach GitHub, opens the
// issue for it. The record is written FIRST and survives every later failure:
// losing the evidence because a CLI was missing is the worst of both worlds.
// A failure to open the issue is reported to w and is not an error — but it
// is reported IN GH'S OWN WORDS, because "no issue opened" leaves the
// operator guessing between a missing label, no auth, and no network.
func RecordEscape(o EscapeOptions, w io.Writer) (EscapeRecord, error) {
	reason := strings.TrimSpace(o.Reason)
	if reason == "" {
		return EscapeRecord{}, fmt.Errorf("an escape needs a reason: what got through, in one line")
	}
	kind := o.Kind
	if kind == "" {
		kind = EscapeKind
	}
	if kind != EscapeKind && kind != FalsePositiveKind {
		return EscapeRecord{}, fmt.Errorf("kind is %q or %q, got %q", EscapeKind, FalsePositiveKind, kind)
	}
	now := time.Now().UTC()
	r := EscapeRecord{
		ID:          escapeID(now, reason),
		Kind:        kind,
		Reason:      reason,
		FromCI:      o.FromCI,
		Evidence:    o.Evidence,
		At:          now,
		Labels:      o.Labels,
		Check:       o.Check,
		ClosesBy:    o.ClosesBy,
		Fingerprint: o.Fingerprint,
	}
	if err := appendEscape(r); err != nil {
		return EscapeRecord{}, err
	}
	if url, number, err := openEscapeIssue(o.Repo, r); err == nil {
		r.Issue, r.Number = url, number
		updateEscape(r)
	} else if !errors.Is(err, errNoIssueTarget) {
		fmt.Fprintf(w, "escape %s: %v\n", r.ID, err)
	}
	return r, nil
}

// escapeID names a record stably: when it happened, plus a short hash of what
// it was, so two records never collide and a re-read finds the same one.
func escapeID(at time.Time, reason string) string {
	sum := sha256.Sum256([]byte(reason))
	return fmt.Sprintf("%d-%s", at.UnixNano(), hex.EncodeToString(sum[:4]))
}

func appendEscape(r EscapeRecord) error {
	path := EscapeLogPath()
	if path == "" {
		return fmt.Errorf("no state dir, so nowhere to record an escape")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// loadEscapes reads every record. A line that does not parse is SKIPPED, not
// fatal: the file is append-only and a torn write must not hide the rest.
func loadEscapes() []EscapeRecord {
	path := EscapeLogPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []EscapeRecord
	for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r EscapeRecord
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// updateEscape rewrites the file with one record replaced by ID. The file is
// small (one line per escape ever recorded) and rewritten rarely.
func updateEscape(updated EscapeRecord) {
	path := EscapeLogPath()
	if path == "" {
		return
	}
	var b strings.Builder
	for _, r := range loadEscapes() {
		if r.ID == updated.ID {
			r = updated
		}
		if data, err := json.Marshal(r); err == nil {
			b.Write(data)
			b.WriteByte('\n')
		}
	}
	// By rename: `list`, `sync` and the weekly digest all read this file, and
	// a torn read would drop escape debt on the floor.
	_ = writeFileAtomic(path, []byte(b.String()))
}

// OpenEscapes is the count of records with no fix yet and the age of the
// oldest — the two numbers the weekly line and `gate stats` print.
func OpenEscapes() (int, time.Duration) {
	n := 0
	var oldest time.Time
	for _, r := range loadEscapes() {
		if r.Closed {
			continue
		}
		n++
		if oldest.IsZero() || r.At.Before(oldest) {
			oldest = r.At
		}
	}
	if n == 0 {
		return 0, 0
	}
	return n, time.Since(oldest)
}

// SyncEscapes opens an issue for every record that has none yet — the
// catch-up path for everything recorded while gh was missing or offline —
// and reconciles the other direction: a record whose issue GitHub already
// closed is marked closed locally, so nothing keeps counting it as open debt
// forever because nothing here polls GitHub on its own (issue #113). It
// returns how many issues it opened.
func SyncEscapes(repo string, w io.Writer) (int, error) {
	opened := 0
	for _, r := range loadEscapes() {
		if r.Closed || r.Issue != "" {
			continue
		}
		url, number, err := openEscapeIssue(repo, r)
		if err != nil {
			fmt.Fprintf(w, "escape %s: could not open an issue: %v\n", r.ID, err)
			continue
		}
		r.Issue, r.Number = url, number
		updateEscape(r)
		opened++
		fmt.Fprintf(w, "escape %s -> %s\n", r.ID, url)
	}
	if closed := syncClosedEscapes(repo); closed > 0 {
		fmt.Fprintf(w, "closed %d locally (already closed on GitHub)\n", closed)
	}
	return opened, nil
}

// syncClosedEscapes marks every synced-but-not-yet-closed local record
// closed once its GitHub issue actually is — one pair of calls rather than
// one per record: every issue this loop ever opened carries the escape or
// false-positive label, whichever repo theme it also carries.
//
// Only records pointing INTO the repo being queried take part. An issue
// number is meaningless on its own (number 9 exists in every repository) and
// the store holds records opened against several, so a closed issue 9 here
// must not close a record whose issue 9 is still open elsewhere.
func syncClosedEscapes(repo string) int {
	slug := githubSlug(repo)
	if slug == "" {
		return 0
	}
	pending := map[string]bool{} // record ID -> waiting on this repo's GitHub
	for _, r := range loadEscapes() {
		if !r.Closed && r.Number > 0 && githubSlugFromURL(r.Issue) == slug {
			pending[r.ID] = true
		}
	}
	if len(pending) == 0 || !ghAvailable() {
		return 0
	}
	closedNow := closedEscapeIssues(repo)
	n := 0
	for _, r := range loadEscapes() {
		if !pending[r.ID] || !closedNow[r.Number] {
			continue
		}
		r.Closed = true
		updateEscape(r)
		n++
	}
	return n
}

// closedEscapeIssues asks GitHub which of its escape issues are closed, ONE
// QUERY PER KIND LABEL. Not one query carrying both: gh reads repeated
// --label flags as an AND, and no escape issue carries both kinds, so the
// two-label query answered [] for every store — measured on this repo as
// "closed nothing" while all 39 issues its records pointed at were closed on
// GitHub. A label whose query fails contributes nothing, and when every
// query fails the result is empty: a fetch that failed reconciles nothing
// rather than guessing.
func closedEscapeIssues(repo string) map[int]bool {
	closed := map[int]bool{}
	for _, label := range []string{EscapeKind, FalsePositiveKind} {
		out, err := runGh(repo, "issue", "list", "--label", label,
			"--state", "all", "--limit", "1000", "--json", "number,state")
		if err != nil {
			continue
		}
		for number := range closedIssueNumbers(out) {
			closed[number] = true
		}
	}
	return closed
}

// githubSlug is the owner/name repo pushes to on GitHub, "" when it does not
// push to GitHub at all — which is also the answer to "is there a GitHub
// remote here" that syncClosedEscapes needs before it asks anything.
func githubSlug(repo string) string {
	if repo == "" {
		return ""
	}
	out, err := gitRead(repo, "remote", "-v")
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(out, "\n") {
		for _, field := range strings.Fields(line) {
			if slug := githubSlugFromURL(field); slug != "" {
				return slug
			}
		}
	}
	return ""
}

// githubSlugFromURL reads owner/name out of any GitHub URL spelling: an ssh
// remote (git@github.com:o/r.git), an https one, and the issue link a record
// carries all name the same repository and must compare equal.
func githubSlugFromURL(link string) string {
	i := strings.Index(link, "github.com")
	if i < 0 {
		return ""
	}
	parts := strings.Split(strings.TrimLeft(link[i+len("github.com"):], "/:"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return strings.ToLower(parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"))
}

// closedIssueNumbers reads a `gh issue list --json number,state` payload into
// the numbers currently CLOSED. nil for a payload that does not parse — a
// fetch that failed reconciles nothing rather than guessing.
func closedIssueNumbers(out string) map[int]bool {
	start, end := strings.Index(out, "["), strings.LastIndex(out, "]")
	if start < 0 || end < start {
		return nil
	}
	var docs []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	if json.Unmarshal([]byte(out[start:end+1]), &docs) != nil {
		return nil
	}
	closed := map[int]bool{}
	for _, d := range docs {
		if strings.EqualFold(d.State, "CLOSED") {
			closed[d.Number] = true
		}
	}
	return closed
}

// ListEscapes prints the records, oldest first: only the open ones by
// default, every one (open and closed) when all is set.
func ListEscapes(w io.Writer, all bool) {
	n := 0
	for _, r := range loadEscapes() {
		if r.Closed && !all {
			continue
		}
		n++
		where := r.Issue
		if where == "" {
			where = "(not synced)"
		}
		status := ""
		if r.Closed {
			status = "  closed"
		}
		fmt.Fprintf(w, "%s  %-14s  %3dd  %s  %s%s\n",
			r.ID, r.Kind, int(time.Since(r.At).Hours()/24), where, r.Reason, status)
	}
	if n == 0 {
		fmt.Fprintln(w, "no open escapes")
	}
}

// issueFingerprintKey marks the fingerprint line in an escape issue's body.
// The local dedupe store lives in this box's gate-state, which an ephemeral
// CI runner does not have — so the ISSUE carries the fingerprint, and a
// runner with no memory can ask GitHub what it cannot remember.
const issueFingerprintKey = "gate-fingerprint:"

// escapeIssueBody is the fixed template. Every escape states the same three
// things, and the last one is what verify-closure judges the fix against.
func escapeIssueBody(r EscapeRecord) string {
	evidence := strings.TrimSpace(r.Evidence)
	if r.FromCI != "" {
		evidence = strings.TrimSpace("caught by " + r.FromCI + "\n\n" + evidence)
	}
	if evidence == "" {
		evidence = "none recorded"
	}
	// The recorder can name the check itself. A defect found by hand is an
	// escape only when SOME check could have seen it, so `--check` fills this
	// line, and a blank means the reader still has to answer it.
	stage := "_name it: a ratchet law, a gate stage, or a demoted check_"
	if c := strings.TrimSpace(r.Check); c != "" {
		stage = c
	}
	// The closes-by line is what verify-closure judges the fix against. An
	// unfilled placeholder refuses every fix that does not restate the check
	// itself, so a recorder that already knows the answer says it here.
	closesBy := "law | stage | demote check X"
	if c := strings.TrimSpace(r.ClosesBy); c != "" {
		closesBy = c
	}
	return fmt.Sprintf(`**What got through:** %s

**Which stage should have caught it:** %s

**Evidence:** %s

closes-by: %s

%s %s

Closing this needs a change to a check — a law under `+"`.ratchet/laws/`"+`, a gate
stage, or a test named on a closes-by line — this one, or one on the fixing PR
or commit. A sentence in a document does not close an escape.
`, r.Reason, stage, evidence, closesBy, issueFingerprintKey, r.Fingerprint)
}

// escapeIssueTitle keeps the subject short enough to read in a list. It cuts
// on RUNES: a byte slice through a multibyte character puts a replacement
// character in the title of every escape recorded in prose.
func escapeIssueTitle(r EscapeRecord) string {
	return r.Kind + ": " + fitRunes(r.Reason, 80)
}

// fitRunes shortens s to at most n runes, marking that it was cut.
func fitRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}

// --- the GitHub half -------------------------------------------------------

// ghAvailable reports whether the GitHub CLI is installed. A var so a test
// can state "absent" without emptying PATH.
var ghAvailable = func() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// runGh runs the GitHub CLI in dir and returns its stdout. A failure carries
// gh's own STDERR: "exit status 1" names none of the three things that
// actually go wrong here (a label that does not exist, no auth, no network),
// and the operator cannot act on a verdict that does not say which.
func runGh(dir string, args ...string) (string, error) {
	return runGhTimeout(dir, 0, args...)
}

// runGhTimeout is runGh with a deadline. Zero means none: the escape verbs
// are typed by a human who can see them run, while the session-start line is
// on a path nothing is allowed to stall.
func runGhTimeout(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.Background(), context.CancelFunc(func() {})
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// stderr-ok: cmd.Stderr above captures it, and the error below folds it in
	out, err := cmd.Output()
	if err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return string(out), fmt.Errorf("gh %s: %w: %s", args[0], err, fitRunes(said, 400))
		}
		return string(out), fmt.Errorf("gh %s: %w", args[0], err)
	}
	return string(out), nil
}

// hasGitHubRemote reports whether repo pushes to GitHub. A repo that does not
// gets the local record and nothing else — there is nowhere to open an issue.
func hasGitHubRemote(repo string) bool {
	if repo == "" {
		return false
	}
	out, err := gitRead(repo, "remote", "-v")
	if err != nil {
		return false
	}
	return strings.Contains(out, "github.com")
}

// errNoIssueTarget means there was nothing to open an issue WITH — no repo,
// no gh, no GitHub remote. It is not a failure: the local record already
// holds the evidence and `gate escape sync` catches up later, so callers
// report every other error and stay quiet about this one.
var errNoIssueTarget = errors.New("no gh, or no GitHub remote")

// openEscapeIssue opens the labelled issue for one record, through the shared
// writer. The kind is always a label; a theme (`--label physics`) rides
// alongside it so a playtest defect lands in the project's own filter as well
// as the escape one.
func openEscapeIssue(repo string, r EscapeRecord) (url string, number int, err error) {
	return OpenIssue(IssueOptions{
		Repo:   repo,
		Title:  escapeIssueTitle(r),
		Body:   escapeIssueBody(r),
		Labels: append([]string{r.Kind}, r.Labels...),
		// A theme the recorder named was judged against the declared list by
		// the command that parsed it; re-judging it here would refuse a sync
		// of a record made with --new-label.
		AllowNewLabel: true,
		LabelMeta: map[string]labelMeta{
			EscapeKind:        {description: escapeLabelDescription(EscapeKind), colour: escapeLabelColour(EscapeKind)},
			FalsePositiveKind: {description: escapeLabelDescription(FalsePositiveKind), colour: escapeLabelColour(FalsePositiveKind)},
		},
	})
}

func escapeLabelDescription(kind string) string {
	if kind == FalsePositiveKind {
		return "a check that refused correct work"
	}
	return "a red the gate should have caught and did not"
}

func escapeLabelColour(kind string) string {
	if kind == FalsePositiveKind {
		return "fbca04"
	}
	return "d73a4a"
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

func issueNumberFromURL(url string) int {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(url[i+1:]))
	if err != nil {
		return 0
	}
	return n
}
