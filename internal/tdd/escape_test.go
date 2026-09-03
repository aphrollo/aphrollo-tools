package tdd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The stub is a compiled binary, not a shell script: Windows cannot exec a
// .bat through CreateProcess, so a script stub would simply never run and
// every assertion about gh would pass vacuously. One binary serves every
// test — it reads what to print, and where to record its argv, from the
// environment.
const ghStubSource = `package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if log := os.Getenv("GH_STUB_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
	}
	if ms := os.Getenv("GH_STUB_SLEEP_MS"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil {
			time.Sleep(time.Duration(n) * time.Millisecond)
		}
	}
	key := ""
	out := ""
	if len(os.Args) > 2 {
		key = "GH_STUB_" + strings.ToUpper(os.Args[1]) + "_" + strings.ToUpper(os.Args[2])
		out = os.Getenv(key)
	}
	if out == "" {
		out = os.Getenv("GH_STUB_OUT")
	}
	if out != "" {
		fmt.Println(out)
	}
	if boom := os.Getenv(key + "_FAIL"); boom != "" {
		fmt.Fprintln(os.Stderr, boom)
		os.Exit(1)
	}
}
`

// ghStubDir builds the stub once for the whole package.
var ghStubDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aphrollo-gh-stub")
	if err != nil {
		return "", err
	}
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(ghStubSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module ghstub\n\ngo 1.26\n"), 0o644); err != nil {
		return "", err
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), ".")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the gh stub: %v\n%s", err, out)
	}
	return dir, nil
})

// stubGh puts the fake gh on PATH with one canned stdout for every call.
func stubGh(t *testing.T, stdout string) (argvLog string) {
	t.Helper()
	return stubGhScript(t, map[string]string{"": stdout})
}

// stubGhScript puts the fake gh on PATH with one canned response per
// `<verb> <noun>` pair — verify-closure asks gh three different questions in
// one run. The empty key is the catch-all.
func stubGhScript(t *testing.T, responses map[string]string) (argvLog string) {
	t.Helper()
	dir, err := ghStubDir()
	if err != nil {
		t.Fatal(err)
	}
	argvLog = filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("GH_STUB_LOG", argvLog)
	for k, v := range responses {
		if k == "" {
			t.Setenv("GH_STUB_OUT", v)
			continue
		}
		verb, noun, _ := strings.Cut(k, " ")
		t.Setenv("GH_STUB_"+strings.ToUpper(verb)+"_"+strings.ToUpper(noun), v)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvLog
}

// makeGitHubRepo is a committed repo whose origin is on GitHub, so the escape
// loop believes there is somewhere to open an issue.
func makeGitHubRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	gitDo(t, root, "remote", "add", "origin", "https://github.com/o/r.git")
	return root
}

func ghArgv(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	return string(data)
}

func readEscapes(t *testing.T) []EscapeRecord {
	t.Helper()
	data, err := os.ReadFile(EscapeLogPath())
	if err != nil {
		return nil
	}
	var out []EscapeRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r EscapeRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("escapes.jsonl line is not JSON: %v (%s)", err, line)
		}
		out = append(out, r)
	}
	return out
}

// A red after a local green is the only evidence the gate has that it is
// missing a check. Losing it means the same class escapes again next month,
// so it is written down before anything else can fail.
func TestRecordEscapeWritesASchemaStampedRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	if _, err := RecordEscape(EscapeOptions{Reason: "CI caught a clippy warning the gate did not"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	recs := readEscapes(t)
	if len(recs) != 1 {
		t.Fatalf("recorded %d escapes, want 1", len(recs))
	}
	r := recs[0]
	if r.Schema != StateSchema {
		t.Errorf("schema = %d, want %d", r.Schema, StateSchema)
	}
	if r.Kind != EscapeKind {
		t.Errorf("kind = %q, want %q by default", r.Kind, EscapeKind)
	}
	if r.Reason != "CI caught a clippy warning the gate did not" || r.At.IsZero() {
		t.Errorf("record = %+v", r)
	}
}

func TestRecordEscapeRefusesAnUnknownKind(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if _, err := RecordEscape(EscapeOptions{Reason: "x", Kind: "whatever"}, io.Discard); err == nil {
		t.Fatal("an unknown kind must be refused, not silently recorded")
	}
}

// The issue is what makes the count go down: a line in a local file is a note
// nobody sees. The body is fixed so every escape states the same three
// things, and closes-by is what verify-closure later judges.
func TestRecordEscapeOpensALabelledIssue(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGh(t, "https://github.com/o/r/issues/42")

	r, err := RecordEscape(EscapeOptions{
		Reason:   "clippy warning reached main",
		Repo:     repo,
		FromCI:   "build (ubuntu-latest)",
		Evidence: "warning: unused variable `x`",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	argv := ghArgv(t, log)
	for _, want := range []string{"issue", "create", "--label", EscapeKind} {
		if !strings.Contains(argv, want) {
			t.Errorf("gh argv %q does not carry %q", argv, want)
		}
	}
	for _, want := range []string{"What got through", "Which stage should have caught it", "closes-by"} {
		if !strings.Contains(argv, want) {
			t.Errorf("the issue body must state %q; argv was %q", want, argv)
		}
	}
	if r.Issue != "https://github.com/o/r/issues/42" || r.Number != 42 {
		t.Errorf("the record must remember the issue it opened: %+v", r)
	}
}

// A box with no gh, or a repo with no GitHub remote, still records: losing
// the evidence because a CLI is missing is the worst of both worlds.
func TestRecordEscapeStillRecordsWithoutGh(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	r, err := RecordEscape(EscapeOptions{Reason: "no gh here", Repo: t.TempDir()}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r.Issue != "" {
		t.Errorf("no gh means no issue, got %q", r.Issue)
	}
	if len(readEscapes(t)) != 1 {
		t.Error("the record must survive an absent gh")
	}
}

// Sync is the catch-up path for everything recorded while gh was missing —
// and it must not re-open an issue that already exists.
func TestSyncEscapesOpensOnlyTheUnsyncedOnes(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	// Recorded with no repo to reach — the offline case sync exists for.
	if _, err := RecordEscape(EscapeOptions{Reason: "one"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordEscape(EscapeOptions{Reason: "two"}, io.Discard); err != nil {
		t.Fatal(err)
	}

	log := stubGh(t, "https://github.com/o/r/issues/7")
	var out strings.Builder
	n, err := SyncEscapes(repo, &out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("synced %d, want 2", n)
	}
	if c := strings.Count(ghArgv(t, log), "issue create"); c != 2 {
		t.Fatalf("gh ran `issue create` %d times, want 2:\n%s", c, ghArgv(t, log))
	}

	again, err := SyncEscapes(repo, &out)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("a second sync opened %d more issues; a synced record is done", again)
	}
}

// A record whose issue closed on GitHub — a fix landed, the PR merged — must
// stop counting as open debt locally: nothing here polls GitHub on its own,
// so nothing ever learned an issue closed until sync reconciled it (issue
// #113).
func TestSyncEscapes_MarksARecordClosedWhenItsIssueClosedOnGitHub(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		Schema: StateSchema, ID: "x", Kind: EscapeKind, Reason: "already fixed",
		At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9,
	}); err != nil {
		t.Fatal(err)
	}
	stubGhScript(t, map[string]string{"issue list": `[{"number":9,"state":"CLOSED"}]`})

	var out strings.Builder
	if _, err := SyncEscapes(repo, &out); err != nil {
		t.Fatal(err)
	}

	recs := readEscapes(t)
	if len(recs) != 1 || !recs[0].Closed {
		t.Fatalf("records = %+v, want the record marked closed", recs)
	}
	if open, _ := OpenEscapes(); open != 0 {
		t.Fatalf("open escapes = %d, want 0 once GitHub shows it closed", open)
	}
	if !strings.Contains(out.String(), "closed 1 locally (already closed on GitHub)") {
		t.Fatalf("output = %q, want the count of records just reconciled", out.String())
	}
}

// A record whose issue is STILL OPEN on GitHub is left alone — sync closes a
// loop, it does not guess one shut.
func TestSyncEscapes_LeavesAStillOpenIssueAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		Schema: StateSchema, ID: "x", Kind: EscapeKind, Reason: "still open",
		At: time.Now().UTC(), Issue: "https://github.com/o/r/issues/9", Number: 9,
	}); err != nil {
		t.Fatal(err)
	}
	stubGhScript(t, map[string]string{"issue list": `[{"number":9,"state":"OPEN"}]`})

	var out strings.Builder
	if _, err := SyncEscapes(repo, &out); err != nil {
		t.Fatal(err)
	}

	recs := readEscapes(t)
	if len(recs) != 1 || recs[0].Closed {
		t.Fatalf("records = %+v, want the still-open record left alone", recs)
	}
	if strings.Contains(out.String(), "closed") {
		t.Fatalf("output = %q, nothing was reconciled — it must not claim otherwise", out.String())
	}
}

// A record that was never synced carries no GitHub issue number (0), so it
// has nothing on GitHub to be closed BY — it must not even cost a
// `gh issue list` call, let alone be reported reconciled.
func TestSyncClosedEscapes_SkipsRecordsWithNoIssueNumber(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	if err := appendEscape(EscapeRecord{
		Schema: StateSchema, ID: "x", Kind: EscapeKind, Reason: "never synced",
		At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	log := stubGh(t, `[]`)

	if n := syncClosedEscapes(repo); n != 0 {
		t.Fatalf("syncClosedEscapes = %d, want 0 — the record was never synced", n)
	}
	if strings.Contains(ghArgv(t, log), "issue list") {
		t.Fatalf("gh was called (%s), want no call for a record with no issue number", ghArgv(t, log))
	}
}

// The count only goes down, so it has to be a number somebody sees.
func TestOpenEscapesCountsTheUnclosedAndTheOldest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	old := EscapeRecord{Schema: StateSchema, ID: "a", Kind: EscapeKind, Reason: "old", At: time.Now().UTC().Add(-30 * 24 * time.Hour)}
	recent := EscapeRecord{Schema: StateSchema, ID: "b", Kind: EscapeKind, Reason: "new", At: time.Now().UTC().Add(-2 * 24 * time.Hour)}
	closed := EscapeRecord{Schema: StateSchema, ID: "c", Kind: EscapeKind, Reason: "done", At: time.Now().UTC().Add(-90 * 24 * time.Hour), Closed: true}
	for _, r := range []EscapeRecord{old, recent, closed} {
		if err := appendEscape(r); err != nil {
			t.Fatal(err)
		}
	}
	n, oldest := OpenEscapes()
	if n != 2 {
		t.Fatalf("open = %d, want 2 (a closed record is paid off)", n)
	}
	if days := int(oldest.Hours() / 24); days != 30 {
		t.Fatalf("oldest = %d days, want 30", days)
	}
}

// The age column is the reason `escape list` exists at all: the oldest
// record is the one owed the most attention, and that only reads right if
// the day count is right.
func TestListEscapes_PrintsTheRecordsAgeInWholeDays(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	if err := appendEscape(EscapeRecord{
		Schema: StateSchema, ID: "a", Kind: EscapeKind, Reason: "ten days old",
		At: time.Now().UTC().Add(-10 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	ListEscapes(&out, false)
	if !strings.Contains(out.String(), "10d") {
		t.Fatalf("output = %q, want the age rendered as 10 whole days", out.String())
	}
}

// `escape list` prints open records by default — the ones somebody still
// owes a fix — and only names a closed one when asked for all of them.
func TestListEscapes_OpenByDefaultAllWithTheFlag(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	for _, r := range []EscapeRecord{
		{Schema: StateSchema, ID: "a", Kind: EscapeKind, Reason: "still open", At: time.Now().UTC()},
		{Schema: StateSchema, ID: "b", Kind: EscapeKind, Reason: "already fixed", At: time.Now().UTC(), Closed: true},
	} {
		if err := appendEscape(r); err != nil {
			t.Fatal(err)
		}
	}

	var openOnly strings.Builder
	ListEscapes(&openOnly, false)
	if !strings.Contains(openOnly.String(), "still open") {
		t.Fatalf("default list = %q, want the open record", openOnly.String())
	}
	if strings.Contains(openOnly.String(), "already fixed") {
		t.Fatalf("default list = %q, want the closed record left out", openOnly.String())
	}

	var all strings.Builder
	ListEscapes(&all, true)
	for _, want := range []string{"still open", "already fixed"} {
		if !strings.Contains(all.String(), want) {
			t.Fatalf("--all list = %q, want %q", all.String(), want)
		}
	}
}

// The managed CLAUDE.md block is where a session reads the rule, so the rule
// has to be in it.
func TestClaudeMDBlockStatesTheEscapeLoop(t *testing.T) {
	block := ClaudeMDBlock("/home/u/bin/cargo-queue", false)
	for _, want := range []string{
		"**Escapes close the loop.**",
		"aphrollo gate escape record",
		"The count only goes down",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the managed block does not state %q", want)
		}
	}
}
