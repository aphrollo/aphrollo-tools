package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
func RecordEscape(o EscapeOptions) (EscapeRecord, error) {
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
	if url, number, ok := openEscapeIssue(o.Repo, r); ok {
		r.Issue, r.Number = url, number
		updateEscape(r)
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
	_ = os.WriteFile(path, []byte(b.String()), 0o600)
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
		url, number, ok := openEscapeIssue(repo, r)
		if !ok {
			fmt.Fprintf(w, "escape %s: could not open an issue (no gh, or no GitHub remote)\n", r.ID)
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

// escapeIssueTitle keeps the subject short enough to read in a list.
func escapeIssueTitle(r EscapeRecord) string {
	reason := r.Reason
	if len(reason) > 80 {
		reason = reason[:77] + "..."
	}
	return r.Kind + ": " + reason
}

// --- the GitHub half -------------------------------------------------------

// ghAvailable reports whether the GitHub CLI is installed. A var so a test
// can state "absent" without emptying PATH.
var ghAvailable = func() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// runGh runs the GitHub CLI in dir and returns its stdout.
func runGh(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	out, err := cmd.Output()
	return string(out), err
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

// openEscapeIssue opens the labelled issue for one record. ok=false means
// there was nothing to open it with, which is never an error: the local
// record already holds the evidence, and `gate escape sync` catches up.
func openEscapeIssue(repo string, r EscapeRecord) (url string, number int, ok bool) {
	if repo == "" || !ghAvailable() || !hasGitHubRemote(repo) {
		return "", 0, false
	}
	out, err := runGh(repo, "issue", "create",
		"--title", escapeIssueTitle(r),
		"--body", escapeIssueBody(r),
		"--label", r.Kind)
	if err != nil {
		return "", 0, false
	}
	url = lastNonEmptyLine(out)
	if !strings.Contains(url, "/issues/") {
		return "", 0, false
	}
	return url, issueNumberFromURL(url), true
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

// --- verify-closure --------------------------------------------------------

// closesRe reads the issue numbers a PR body says it closes, in every
// spelling GitHub accepts.
var closesRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fixe?[sd]?|resolve[sd]?)\s+#(\d+)`)

// checkPathPrefixes are the paths that COUNT as changing a check: a declared
// law, a gate stage, or a workspace's own gate configuration.
var checkPathPrefixes = []string{".ratchet/laws/", "internal/tdd/"}

// VerifyClosure judges a PR that claims to close escapes: each labelled issue
// it closes must be accompanied by a diff that changes a CHECK — a law, a
// gate stage, a workspace's Cargo.toml gate metadata, or a test named on the
// issue's closes-by line. It prints one verdict per issue and reports whether
// all of them passed.
func VerifyClosure(repo, pr string, w io.Writer) (bool, error) {
	if !ghAvailable() {
		return false, fmt.Errorf("verify-closure needs the GitHub CLI (gh) on PATH")
	}
	body, err := ghJSONField(repo, "body", "pr", "view", pr, "--json", "body")
	if err != nil {
		return false, err
	}
	diff, err := runGh(repo, "pr", "diff", pr, "--name-only")
	if err != nil {
		return false, err
	}
	files := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")

	all := true
	judged := 0
	for _, m := range closesRe.FindAllStringSubmatch(body, -1) {
		number := m[1]
		labels, issueBody, err := issueLabelsAndBody(repo, number)
		if err != nil {
			return false, err
		}
		if !labels[EscapeKind] && !labels[FalsePositiveKind] {
			continue // somebody else's issue
		}
		judged++
		if why, ok := closureChangesACheck(files, issueBody); ok {
			fmt.Fprintf(w, "#%s ok — %s\n", number, why)
			continue
		}
		all = false
		fmt.Fprintf(w, "#%s FAIL — the PR changes no check: an escape closes with a law under .ratchet/laws/, a gate stage, gate metadata, or a test named on its closes-by line\n", number)
	}
	if judged == 0 {
		fmt.Fprintf(w, "PR #%s closes no escape issue\n", pr)
	}
	return all, nil
}

// closureChangesACheck reports whether the PR's file list touches something
// that actually judges code, naming what it found.
func closureChangesACheck(files []string, issueBody string) (string, bool) {
	named := closesByFiles(issueBody)
	for _, f := range files {
		rel := strings.TrimSpace(strings.ReplaceAll(f, `\`, "/"))
		if rel == "" {
			continue
		}
		for _, prefix := range checkPathPrefixes {
			if strings.HasPrefix(rel, prefix) {
				return rel, true
			}
		}
		if strings.HasSuffix(rel, "/Cargo.toml") || rel == "Cargo.toml" {
			return rel + " (gate metadata)", true
		}
		if named[rel] {
			return rel + " (named on closes-by)", true
		}
	}
	return "", false
}

// closesByFiles reads the paths an issue's closes-by line names, so a fix
// that lands as a TEST can say which test and be judged on it.
func closesByFiles(issueBody string) map[string]bool {
	out := map[string]bool{}
	for line := range strings.SplitSeq(strings.ReplaceAll(issueBody, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(t), "closes-by:") {
			continue
		}
		for _, tok := range strings.FieldsFunc(t, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ',' || r == '|' || r == '`'
		}) {
			if strings.Contains(tok, "/") && strings.Contains(tok, ".") {
				out[strings.ReplaceAll(tok, `\`, "/")] = true
			}
		}
	}
	return out
}

// issueLabelsAndBody reads one issue's labels and body through gh.
func issueLabelsAndBody(repo, number string) (map[string]bool, string, error) {
	out, err := runGh(repo, "issue", "view", number, "--json", "labels,body")
	if err != nil {
		return nil, "", err
	}
	var doc struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &doc); err != nil {
		return nil, "", fmt.Errorf("reading issue #%s: %w", number, err)
	}
	labels := map[string]bool{}
	for _, l := range doc.Labels {
		labels[l.Name] = true
	}
	return labels, doc.Body, nil
}

// ghJSONField reads one string field out of a gh --json response.
func ghJSONField(repo, field string, args ...string) (string, error) {
	out, err := runGh(repo, args...)
	if err != nil {
		return "", err
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &doc); err != nil {
		return "", fmt.Errorf("reading %s: %w", field, err)
	}
	s, _ := doc[field].(string)
	return s, nil
}

// firstJSONObject trims whatever a shell stub prints around the payload — a
// `.bat` stub cannot avoid a trailing newline, and a real gh may print a
// notice before it.
func firstJSONObject(out string) string {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return strings.TrimSpace(out)
	}
	return out[start : end+1]
}
