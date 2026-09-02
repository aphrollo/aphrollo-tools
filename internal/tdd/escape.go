package tdd

import (
	"bytes"
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
	"sync"
	"time"
)

// An ESCAPE is a red that arrived after a local green: CI failed on a commit
// the gate passed, a merge gate refused what precommit allowed, a mutant
// survived, a playtest found a defect some check could have seen. Each one is
// the only direct evidence the gate has about what it is missing, and each
// one is normally lost — noticed in a terminal, fixed, forgotten, and the
// same class escapes again a month later.
//
// So an escape is RECORDED (a schema-stamped line in escapes.jsonl) and, when
// there is a GitHub remote and a `gh` to reach it, opened as a labelled issue
// with a fixed body. The body's third line is the whole point: an escape is
// closed by a LAW or a STAGE named in the fix, never by a sentence in a
// document. `verify-closure` is what enforces that at the PR, and the count
// is printed weekly at session start so it stays a number somebody owns.
//
// A false positive is the same loop pointing the other way: a check that
// refuses correct work is debt too, and the record is what turns "this rule
// is annoying" into a demotion somebody can defend.

const (
	// EscapeKind is a red the gate should have caught and did not.
	EscapeKind = "escape"
	// FalsePositiveKind is a check that refused work which was correct.
	FalsePositiveKind = "false-positive"
)

// EscapeRecord is one entry in escapes.jsonl.
type EscapeRecord struct {
	Schema   int       `json:"schema"`
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Reason   string    `json:"reason"`
	FromCI   string    `json:"from_ci,omitempty"`
	Evidence string    `json:"evidence,omitempty"`
	At       time.Time `json:"at"`
	Issue    string    `json:"issue,omitempty"`
	Number   int       `json:"number,omitempty"`
	Closed   bool      `json:"closed,omitempty"`
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
		Schema:   StateSchema,
		ID:       escapeID(now, reason),
		Kind:     kind,
		Reason:   reason,
		FromCI:   o.FromCI,
		Evidence: o.Evidence,
		At:       now,
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
// catch-up path for everything recorded while gh was missing or offline. It
// returns how many it opened.
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
	return opened, nil
}

// ListEscapes prints every open record, oldest first.
func ListEscapes(w io.Writer) {
	n := 0
	for _, r := range loadEscapes() {
		if r.Closed {
			continue
		}
		n++
		where := r.Issue
		if where == "" {
			where = "(not synced)"
		}
		fmt.Fprintf(w, "%s  %-14s  %3dd  %s  %s\n",
			r.ID, r.Kind, int(time.Since(r.At).Hours()/24), where, r.Reason)
	}
	if n == 0 {
		fmt.Fprintln(w, "no open escapes")
	}
}

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
	return fmt.Sprintf(`**What got through:** %s

**Which stage should have caught it:** _name it: a ratchet law, a gate stage, or a demoted check_

**Evidence:** %s

closes-by: law | stage | demote check X

Closing this needs a change to a check — a law under `+"`.ratchet/laws/`"+`, a gate
stage, or a test named on the closes-by line. A sentence in a document does not
close an escape.
`, r.Reason, evidence)
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
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
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

// openEscapeIssue opens the labelled issue for one record.
func openEscapeIssue(repo string, r EscapeRecord) (url string, number int, err error) {
	if repo == "" || !ghAvailable() || !hasGitHubRemote(repo) {
		return "", 0, errNoIssueTarget
	}
	ensureEscapeLabel(repo, r.Kind)
	out, err := runGh(repo, "issue", "create",
		"--title", escapeIssueTitle(r),
		"--body", escapeIssueBody(r),
		"--label", r.Kind)
	if err != nil {
		return "", 0, err
	}
	url = lastNonEmptyLine(out)
	if !strings.Contains(url, "/issues/") {
		return "", 0, fmt.Errorf("gh issue create printed no issue URL: %q", fitRunes(strings.TrimSpace(out), 200))
	}
	return url, issueNumberFromURL(url), nil
}

// labelEnsured remembers which labels this process has already created, so a
// sync of twenty escapes does not make twenty identical API calls.
var labelEnsured sync.Map

// ensureEscapeLabel creates the label the issue is about to ask for. A fresh
// repository has neither `escape` nor `false-positive`, and `gh issue create
// --label` FAILS outright on a label that does not exist — so without this
// every escape in a new repo is recorded locally and reaches nobody.
//
// `--force` makes it idempotent (it updates the existing label instead of
// failing), and a failure here is deliberately ignored: the issue create that
// follows is the real test of whether the label is usable, and it reports in
// gh's own words.
func ensureEscapeLabel(repo, kind string) {
	key := repo + "\x00" + kind
	if _, done := labelEnsured.LoadOrStore(key, true); done {
		return
	}
	_, _ = runGh(repo, "label", "create", kind, "--force",
		"--description", escapeLabelDescription(kind),
		"--color", escapeLabelColour(kind))
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
