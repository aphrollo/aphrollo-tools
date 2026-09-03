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

// A mutation run started inside an agent's tool shell died twice at mutant
// 101 of 131 with exit 1 and no error text — the shell's timeout killed the
// process tree, and cargo-mutants has no resume, so 100 verdicts were thrown
// away each time (issue #103). Three things close that:
//
//	detached — the job is started outside any shell's process tree, with its
//	   own stdout/stderr FILES, so a timeout aimed at the tool call cannot
//	   reach it and the reason it stopped is written down.
//	resumed — cargo-mutants writes each verdict to mutants.out as it reaches
//	   it, so an interrupted run's answers are already on disk. They are read
//	   back, kept per tip tree, and excluded from the restart: a second run
//	   measures what is left, not the whole diff again.
//	accounted — a run that ends with no receipt records what it exited with
//	   and the last lines of its stderr, and the merge that then finds no
//	   receipt says so instead of "run it again".

// mutantsOutFiles are the verdict files cargo-mutants keeps in mutants.out,
// each holding one mutant per line.
var mutantsOutFiles = []string{"caught", "missed", "timeout", "unviable"}

// mutantLineRe reads one of those lines. The real shape, from a
// cargo-mutants 27.1.0 run, is "<file>:<line>:<col>: <mutation>":
//
//	crates/editor_client/src/creator.rs:101:33: replace || with && in send_undo_redo
//
// The column is captured because it is part of the mutant's IDENTITY — the
// same file names two `replace || with &&` mutants on line 101, at columns 33
// and 16 — and the whole line is kept verbatim because that string, not a
// rebuilt one, is what `--exclude-re` has to match on a resumed run.
var mutantLineRe = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.+)$`)

// readMutantsOut reads every verdict an interrupted run reached, from the
// files it wrote as it went.
func readMutantsOut(worktree string) []MutantOutcome {
	var out []MutantOutcome
	for _, status := range mutantsOutFiles {
		f, err := os.Open(filepath.Join(worktree, "mutants.out", status+".txt"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if m, ok := parseMutantLine(strings.TrimSpace(sc.Text())); ok {
				m.Status = status
				out = append(out, m)
			}
		}
		f.Close()
	}
	return out
}

func parseMutantLine(line string) (MutantOutcome, bool) {
	m := mutantLineRe.FindStringSubmatch(line)
	if m == nil {
		return MutantOutcome{}, false
	}
	lineNo, lineErr := strconv.Atoi(m[2])
	col, colErr := strconv.Atoi(m[3])
	if lineErr != nil || colErr != nil {
		return MutantOutcome{}, false
	}
	return MutantOutcome{
		File:     filepath.ToSlash(m[1]),
		Line:     lineNo,
		Col:      col,
		Mutation: strings.TrimSpace(m[4]),
		Name:     line,
	}, true
}

// mutantLineOf is the tool's own spelling of a mutant, for a producer that
// reports its parts rather than a line.
func mutantLineOf(file string, line, col int, mutation string) string {
	return fmt.Sprintf("%s:%d:%d: %s", file, line, col, mutation)
}

// name is how a receipt names this mutant.
func (m MutantOutcome) name() MutantName {
	return MutantName{File: m.File, Line: m.Line, Col: m.Col, Mutation: m.Mutation, Raw: m.Name}
}

// ResumeMutants splits a tip's mutant list by whether an earlier, interrupted
// run already reached a verdict for it.
func ResumeMutants(all, judged []MutantOutcome) (run, carry []MutantOutcome) {
	have := map[mutantKey]MutantOutcome{}
	for _, m := range judged {
		have[m.key()] = m
	}
	for _, m := range all {
		if old, ok := have[m.key()]; ok {
			carry = append(carry, old)
			continue
		}
		run = append(run, m)
	}
	return run, carry
}

// mutantsPartialPath keeps one tip tree's reached verdicts, so a restart of
// THAT tree resumes and a different tree never inherits them.
func mutantsPartialPath(tree string) string {
	dir := mutantsStateDir()
	if dir == "" || tree == "" {
		return ""
	}
	return filepath.Join(dir, "partial."+tree+".json")
}

// saveMutantsPartials records the verdicts reached so far, merged with
// whatever an earlier attempt on the same tree had already reached.
func saveMutantsPartials(tree string, reached []MutantOutcome) {
	path := mutantsPartialPath(tree)
	if path == "" {
		return
	}
	merged := map[mutantKey]MutantOutcome{}
	for _, m := range loadMutantsPartials(tree) {
		merged[m.key()] = m
	}
	for _, m := range reached {
		merged[m.key()] = m
	}
	out := make([]MutantOutcome, 0, len(merged))
	for _, m := range merged {
		out = append(out, m)
	}
	sortOutcomes(out)
	data, err := json.Marshal(out)
	if err != nil {
		return
	}
	_ = writeFileAtomic(path, data)
}

// loadMutantsPartials reads the verdicts an earlier attempt on this tree
// reached, empty when there was none.
func loadMutantsPartials(tree string) []MutantOutcome {
	path := mutantsPartialPath(tree)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []MutantOutcome
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// sortOutcomes puts a receipt's outcomes in one order whatever the run did,
// so two runs of the same tree produce the same bytes.
func sortOutcomes(out []MutantOutcome) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Mutation < b.Mutation
	})
}

// MutantsDeath is what a run that produced no receipt left behind.
type MutantsDeath struct {
	Tree   string    `json:"tree"`
	Exit   int       `json:"exit"`
	At     time.Time `json:"at"`
	ErrLog string    `json:"err_log"`
	Tail   []string  `json:"tail"`
}

func mutantsDeathPath(tree string) string {
	dir := mutantsStateDir()
	if dir == "" || tree == "" {
		return ""
	}
	return filepath.Join(dir, "died."+tree+".json")
}

// recordMutantsDeath writes down that a run ended without a verdict, with the
// last lines of its stderr — the reason has to outlive the process, or the
// next session finds only a missing receipt and no explanation.
func recordMutantsDeath(j MutantsJob, exit int, tail []string) {
	path := mutantsDeathPath(j.TipTree)
	if path == "" {
		return
	}
	d := MutantsDeath{Tree: j.TipTree, Exit: exit, At: time.Now(), ErrLog: j.ErrLog, Tail: tail}
	if data, err := json.Marshal(d); err == nil {
		_ = writeFileAtomic(path, data)
	}
	appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-died:"+short(j.TipTree)+":"+strconv.Itoa(exit), 0)
	for _, line := range tail {
		appendGateLog("mutants", logToken(j.Repo), "mutants-stderr", logToken(line), 0)
	}
}

func loadMutantsDeath(tree string) (MutantsDeath, bool) {
	path := mutantsDeathPath(tree)
	if path == "" {
		return MutantsDeath{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return MutantsDeath{}, false
	}
	var d MutantsDeath
	if err := json.Unmarshal(data, &d); err != nil {
		return MutantsDeath{}, false
	}
	return d, true
}

// clearMutantsDeath forgets a death once the same tree has been measured
// through: an old record left in place would explain a receipt that exists.
func clearMutantsDeath(tree string) {
	if path := mutantsDeathPath(tree); path != "" {
		_ = os.Remove(path)
	}
}

// stderrTail is the last n lines of a run's stderr file — enough to say what
// happened, short enough to put in a log line.
func stderrTail(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lines []string
	for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
