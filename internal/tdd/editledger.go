package tdd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The edit ledger is the edit hook's memory of what each edit was and what
// its run said about it, per checkout. gate.log keeps only a verdict word per
// run and the session state keeps only the LAST outcome, so neither can say
// "this test went red on an edit that touched only test code, and green on a
// later one" — which is the RED proof the commit gate needs for a test that
// shares its file with the code it tests (issue #714), where fail-first
// cannot apply the test alone onto HEAD.
//
// Two record kinds, one JSON object per line, in edit order:
//
//   - an EDIT, written when the hook sees a file change, before any suite
//     runs: which file, on which HEAD, and what the file now holds, split
//     into production and test code (rusttestspans.go) and classified
//     against the file's previous recorded state (or HEAD's copy when the
//     ledger has none) as test-only, production, or unknown;
//   - a VERDICT, written when that edit's run settles — immediately, or at a
//     later hook for a deferred run — carrying the edit's id, the command, the
//     outcome, and the failing and passing test names.
//
// Every record belongs to the HEAD it was made on. The first record written
// on a new HEAD drops every older one: a commit moved the work on, and an
// edit made before it cannot describe the next commit's work.

// Edit classes.
const (
	editTestOnly   = "test-only"
	editProduction = "production"
	editUnknown    = "unknown"
)

// editLedgerCap bounds one checkout's ledger to its most recent edits.
const editLedgerCap = 400

// ledgerEdit is one edit with the verdict its run reached (nil when none
// did: the run is still deferred, timed out, or never started).
type ledgerEdit struct {
	ID      string            `json:"id"`
	At      time.Time         `json:"at"`
	Head    string            `json:"head"`
	File    string            `json:"file"`
	Class   string            `json:"class"`
	Prod    string            `json:"prod,omitempty"`
	Region  string            `json:"region,omitempty"`
	Support string            `json:"support,omitempty"`
	Tests   map[string]string `json:"tests,omitempty"`
	Verdict *ledgerVerdict    `json:"-"`
}

// ledgerVerdict is what one edit's run concluded.
type ledgerVerdict struct {
	Cmd     string   `json:"cmd"`
	Outcome string   `json:"outcome"`
	Failing []string `json:"failing,omitempty"`
	Passed  []string `json:"passed,omitempty"`
}

// ledgerLine is the on-disk shape: exactly one of Edit and Verdict is set.
type ledgerLine struct {
	Edit    *ledgerEdit    `json:"edit,omitempty"`
	EditID  string         `json:"edit_id,omitempty"`
	Verdict *ledgerVerdict `json:"verdict,omitempty"`
}

func editLedgerPath(root string) string {
	dir := stateDir()
	if dir == "" || root == "" {
		return ""
	}
	return filepath.Join(dir, "edit-ledger", repoStateKey(root)+".jsonl")
}

// recordEdit appends an edit record for file (absolute) under root and
// returns its id, "" when nothing could be recorded. Best effort, like
// gate.log: a ledger that cannot be written costs the commit gate a proof,
// never a decision.
func recordEdit(root, file string) string {
	path := editLedgerPath(root)
	if path == "" {
		return ""
	}
	head := headSHAFor(root)
	lines := readLedgerLines(path)
	kept := ledgerOnHead(lines, head)
	e := snapshotEdit(root, file, head, previousState(kept, file))
	if len(kept) != len(lines) {
		kept = capLedger(append(kept, ledgerLine{Edit: &e}))
		if err := rewriteLedger(path, kept); err != nil {
			warnGateLogUnwritable(fmt.Sprintf("edit ledger %s: %v", path, err))
		}
		return e.ID
	}
	appendLedgerLine(path, ledgerLine{Edit: &e})
	return e.ID
}

// recordEditVerdict attaches a settled run's verdict to edit id.
func recordEditVerdict(root, id, cmd string, outcome Outcome, output string) {
	path := editLedgerPath(root)
	if path == "" || id == "" {
		return
	}
	appendLedgerLine(path, ledgerLine{EditID: id, Verdict: &ledgerVerdict{
		Cmd: cmd, Outcome: string(outcome),
		Failing: ExtractFailingTests(output), Passed: ExtractPassingTests(output),
	}})
}

// loadEditLedger returns root's edits in the order they were made, each with
// the last verdict recorded for it.
func loadEditLedger(root string) []ledgerEdit {
	path := editLedgerPath(root)
	if path == "" {
		return nil
	}
	var edits []ledgerEdit
	at := map[string]int{}
	for _, l := range readLedgerLines(path) {
		switch {
		case l.Edit != nil:
			at[l.Edit.ID] = len(edits)
			edits = append(edits, *l.Edit)
		case l.Verdict != nil:
			if i, ok := at[l.EditID]; ok {
				edits[i].Verdict = l.Verdict
			}
		}
	}
	return edits
}

// snapshotEdit reads file as it is now and classifies the change against
// prev, the file's previous known state (nil: compare with HEAD's copy).
func snapshotEdit(root, file, head string, prev *ledgerEdit) ledgerEdit {
	e := ledgerEdit{
		ID:   strconv.FormatInt(time.Now().UnixNano(), 36),
		At:   time.Now().UTC(),
		Head: head, File: file, Class: editUnknown,
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return e
	}
	now, ok := fileSplit(root, file, string(data))
	if !ok {
		return e
	}
	e.Prod, e.Region, e.Support, e.Tests = now.prod, now.region, now.support, now.tests
	before := prev
	if before == nil {
		base, ok := headSplit(root, file, head)
		if !ok {
			return e
		}
		before = &ledgerEdit{Prod: base.prod}
	}
	e.Class = editProduction
	if before.Prod == e.Prod {
		e.Class = editTestOnly
	}
	return e
}

// previousState is the last edit of file in lines whose content was known.
func previousState(lines []ledgerLine, file string) *ledgerEdit {
	for i := len(lines) - 1; i >= 0; i-- {
		if e := lines[i].Edit; e != nil && e.File == file && e.Class != editUnknown {
			return e
		}
	}
	return nil
}

// fileSplit splits one file's content into production and test code by its
// kind: a Rust source file by its #[cfg(test)] items (the whole file when a
// #[cfg(test)] declaration mounts it), a Test-classified file is all test
// code, and any other source file is all production.
func fileSplit(root, file, src string) (rustSplit, bool) {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		// absence-ok: a file with no path under root has no split, and the
		// edit records unknown, which no proof is built on.
		return rustSplit{}, false
	}
	switch {
	case ClassifyFile(rel) == Test:
		if strings.EqualFold(filepath.Ext(file), ".rs") {
			return splitRustTests(src, true)
		}
		return rustSplit{region: hashNonEmpty(src)}, true
	case strings.EqualFold(filepath.Ext(file), ".rs"):
		return splitRustTests(src, rustFileIsTestModule(root, rel, file))
	default:
		return rustSplit{prod: hashNonEmpty(src)}, true
	}
}

// headSplit is fileSplit over HEAD's copy of file. A file HEAD does not have
// splits as empty: everything in it now is new.
func headSplit(root, file, head string) (rustSplit, bool) {
	if head == "" {
		return fileSplit(root, file, "")
	}
	rel, err := filepath.Rel(root, file)
	if err != nil {
		// absence-ok: same as fileSplit, the edit records unknown.
		return rustSplit{}, false
	}
	src, err := git(root, "show", head+":./"+filepath.ToSlash(rel))
	if err == nil {
		return fileSplit(root, file, src)
	}
	if _, cerr := git(root, "cat-file", "-e", head+"^{commit}"); cerr != nil {
		// absence-ok: HEAD itself is unreadable, so the edit records unknown.
		return rustSplit{}, false
	}
	// The commit exists and the path is not in it: HEAD's copy is empty.
	return fileSplit(root, file, "")
}

func readLedgerLines(path string) []ledgerLine {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []ledgerLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var l ledgerLine
		if json.Unmarshal(sc.Bytes(), &l) == nil {
			out = append(out, l)
		}
	}
	return out
}

// ledgerOnHead keeps the records made on head, dropping every verdict whose
// edit went with them.
func ledgerOnHead(lines []ledgerLine, head string) []ledgerLine {
	keep := map[string]bool{}
	var out []ledgerLine
	for _, l := range lines {
		switch {
		case l.Edit != nil && l.Edit.Head == head:
			keep[l.Edit.ID] = true
			out = append(out, l)
		case l.Verdict != nil && keep[l.EditID]:
			out = append(out, l)
		}
	}
	return out
}

// capLedger keeps the newest editLedgerCap edits and their verdicts.
func capLedger(lines []ledgerLine) []ledgerLine {
	edits := 0
	for _, l := range lines {
		if l.Edit != nil {
			edits++
		}
	}
	if edits <= editLedgerCap {
		return lines
	}
	drop := map[string]bool{}
	var out []ledgerLine
	for _, l := range lines {
		if l.Edit != nil && edits > editLedgerCap {
			drop[l.Edit.ID] = true
			edits--
			continue
		}
		if l.Verdict != nil && drop[l.EditID] {
			continue
		}
		out = append(out, l)
	}
	return out
}

func rewriteLedger(path string, lines []ledgerLine) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, l := range lines {
		data, err := json.Marshal(l)
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return writeFileAtomic(path, []byte(b.String()))
}

func appendLedgerLine(path string, l ledgerLine) {
	data, err := json.Marshal(l)
	if err != nil {
		warnGateLogUnwritable(fmt.Sprintf("edit ledger %s: %v", path, err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		warnGateLogUnwritable(fmt.Sprintf("edit ledger %s: %v", path, err))
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		warnGateLogUnwritable(fmt.Sprintf("edit ledger %s: %v", path, err))
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		warnGateLogUnwritable(fmt.Sprintf("edit ledger %s: %v", path, err))
	}
}

// passLineRes extract passing test names from cargo's two report shapes:
// libtest's `test <name> ... ok` and nextest's `PASS [<secs>] <bin> <name>`.
var passLineRes = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^test\s+(\S+)\s+\.\.\.\s+ok[^\S\n]*$`),
	regexp.MustCompile(`(?m)^[^\S\n]*(?:TRY[^\S\n]+\d+[^\S\n]+)?PASS[^\S\n]+\[[^\]]*\][^\S\n]+(?:\S+[^\S\n]+)*(\S+)[^\S\n]*$`),
}

// ExtractPassingTests returns the sorted, de-duplicated set of passing test
// names in cargo runner output.
func ExtractPassingTests(output string) []string {
	seen := map[string]bool{}
	for _, re := range passLineRes {
		for _, m := range re.FindAllStringSubmatch(output, -1) {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
