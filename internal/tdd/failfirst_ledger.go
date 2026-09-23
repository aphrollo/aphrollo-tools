package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// An inline Rust test shares its file with the code it tests, so fail-first
// cannot apply the test alone onto HEAD (precommit_failfirst_rust.go). The
// edit hook saw the RED anyway, and the edit ledger (editledger.go) kept it:
// this file reads that record as the RED proof, and only under all of these:
//
//   - RED: an edit on this checkout's current HEAD that the ledger classified
//     test-only, holding the staged test's exact body, whose run went red
//     naming the test (or failed to compile, naming nothing, which is how a
//     test calling a function that does not exist yet goes red);
//   - GREEN: a later edit whose run passed naming the test;
//   - every edit from the RED up to and including the GREEN changed
//     production code, and none of them touched the test or the test code it
//     leans on (the helpers outside every #[test] fn in its file) — so the
//     green came from the implementation, not from rewriting the test;
//   - the staged file still holds the test and its helpers exactly as they
//     were at the RED.
//
// A test with no qualifying pair leaves the stage exactly as it was:
// inconclusive. The proof never turns an unproven test into a proven one.

// ledgerProof is one test's proof: the edits it relied on.
type ledgerProof struct {
	file, test string
	red, green string
}

// inlineTest is one staged #[test] fn that HEAD does not have.
type inlineTest struct {
	file    string // absolute
	rel     string // repoRoot-relative, for the gate line
	name    string
	body    string // the staged body hash
	support string // the staged file's helper hash
	module  string // the module path cargo names the file's tests under
}

// ledgerRedProofs returns one proof per new inline test in srcs, or ok=false
// when any of them has none (or none could be read).
func ledgerRedProofs(repoRoot, root string, srcs []string) ([]ledgerProof, bool) {
	head := headSHAFor(root)
	if head == "" {
		return nil, false
	}
	tests, ok := stagedInlineTests(repoRoot, root, head, srcs)
	if !ok || len(tests) == 0 {
		return nil, false
	}
	var edits []ledgerEdit
	for _, e := range loadEditLedger(root) {
		if e.Head == head {
			edits = append(edits, e)
		}
	}
	proofs := make([]ledgerProof, 0, len(tests))
	for _, it := range tests {
		p, ok := ledgerProofFor(edits, it)
		if !ok {
			return nil, false
		}
		proofs = append(proofs, p)
	}
	return proofs, true
}

// stagedInlineTests lists the #[test] fns the staged copy of each Rust
// source file in srcs holds and HEAD's copy does not.
func stagedInlineTests(repoRoot, root, head string, srcs []string) ([]inlineTest, bool) {
	var out []inlineTest
	for _, src := range srcs {
		if !strings.EqualFold(filepath.Ext(src), ".rs") {
			continue
		}
		abs := filepath.Join(repoRoot, filepath.FromSlash(src))
		staged, err := git(repoRoot, "show", ":"+src)
		if err != nil {
			// absence-ok: an unreadable staged copy proves nothing; the stage stays inconclusive.
			return nil, false
		}
		now, ok := fileSplit(root, abs, staged)
		if !ok {
			return nil, false
		}
		before, ok := headSplit(root, abs, head)
		if !ok {
			return nil, false
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			// absence-ok: a file outside root has no module path; the stage stays inconclusive.
			return nil, false
		}
		for name, body := range now.tests {
			if _, had := before.tests[name]; had {
				continue
			}
			if body == ambiguousTest {
				return nil, false
			}
			out = append(out, inlineTest{
				file: abs, rel: filepath.ToSlash(src), name: name, body: body,
				support: now.support, module: cargoModuleFilterPath(root, filepath.ToSlash(rel)),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rel != out[j].rel {
			return out[i].rel < out[j].rel
		}
		return out[i].name < out[j].name
	})
	return out, true
}

// ledgerProofFor finds the latest RED/GREEN pair in edits that proves it.
func ledgerProofFor(edits []ledgerEdit, it inlineTest) (ledgerProof, bool) {
	var found ledgerProof
	ok := false
	for r, red := range edits {
		if !ledgerRedFor(red, it) {
			continue
		}
		if g := ledgerGreenAfter(edits, r, it); g >= 0 {
			found = ledgerProof{file: it.rel, test: it.name, red: red.ID, green: edits[g].ID}
			ok = true
		}
	}
	return found, ok
}

// ledgerRedFor reports whether e is a test-only edit, holding the staged
// test and helpers, whose run went red on that test.
func ledgerRedFor(e ledgerEdit, it inlineTest) bool {
	if e.File != it.file || e.Class != editTestOnly || e.Verdict == nil {
		return false
	}
	if e.Tests[it.name] != it.body || e.Support != it.support {
		return false
	}
	switch Outcome(e.Verdict.Outcome) {
	case Red:
		return namesTest(e.Verdict.Failing, it)
	case RedMissingImpl:
		return len(e.Verdict.Failing) == 0 || namesTest(e.Verdict.Failing, it)
	}
	return false
}

// ledgerGreenAfter returns the index of the first edit after r whose run
// passed the test, with every edit on the way (it included) a production
// change that left the test and its helpers alone; -1 when there is none.
func ledgerGreenAfter(edits []ledgerEdit, r int, it inlineTest) int {
	for g := r + 1; g < len(edits); g++ {
		e := edits[g]
		if e.Class != editProduction {
			return -1
		}
		if e.File == it.file && (e.Tests[it.name] != it.body || e.Support != it.support) {
			return -1
		}
		if e.Verdict == nil {
			continue
		}
		switch Outcome(e.Verdict.Outcome) {
		case Green, GreenWithWarnings:
			if namesTest(e.Verdict.Passed, it) {
				return g
			}
		}
	}
	return -1
}

// namesTest reports whether names holds the test by the path cargo reports
// it under: the file's own module path, then any inner module, then its name.
func namesTest(names []string, it inlineTest) bool {
	for _, n := range names {
		if !strings.HasSuffix("::"+n, "::"+it.name) {
			continue
		}
		if it.module == "" || strings.HasPrefix(n, it.module+"::") {
			return true
		}
	}
	return false
}

// reportLedgerProofs prints and logs the stage line for a proof.
func reportLedgerProofs(root string, proofs []ledgerProof) {
	parts := make([]string, 0, len(proofs))
	for _, p := range proofs {
		parts = append(parts, fmt.Sprintf("%s %s: red at edit %s, green at edit %s", p.file, p.test, p.red, p.green))
	}
	fmt.Fprintf(os.Stderr, "[fail-first] gate precommit: postedit ledger in %s → red-proven (%s)\n", root, strings.Join(parts, "; "))
	AppendGateLog("precommit", root, "postedit-ledger", "red-proven", 0)
}
