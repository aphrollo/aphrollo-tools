package tdd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// WHAT A RUN PROVED, AND WHAT IT MAY THEREFORE CLAIM.
//
// borld#394: the commit gate passed a lane whose crates' test suites it never
// ran, and one of them was red. The pass was the design — the touched crates'
// suites moved to the merge gate — but the CLAIM that followed it was not.
// Any green run in the gate flipped one process-wide flag (suiteRanGreen), and
// a workspace's always-run GUARD crate, which owns nothing a commit touches,
// is always one of them. So the post-commit hook wrote `green <tree>` onto a
// commit whose touched crate had not been compiled, let alone tested.
//
// That single lie fed both halves of the escape loop. Downstream, CI read the
// note as "the local gate proved this tree" and the merge gate read it the
// same way: when the merge then ran the suites for the first time and found
// one red, NoteMergeGateEscape saw a note it believed and recorded "the merge
// gate refused a lane whose pre-commit gate had run a suite green on the same
// tree" — borld#309, #363, #384 and #449, four issues in eight days, every one
// of them a merge gate doing its job correctly (a 2006-line file, a red
// bandwidth budget, a test tree that did not compile, a red fuel test) and
// being called a false positive because of what the note claimed.
//
// The law here is runscope.go's, pointed at a claim instead of at a rerun: A
// VERDICT MAY ONLY VOUCH FOR GROUND IT IS AT LEAST AS WIDE AS. A stage that
// declines to run a suite still OWES that scope; a run that executes tests and
// passes PROVES its own; and the tree-wide note is written only when what was
// proved covers what was owed. A guard crate's green stays what it is — a fact
// about the guard crate.

// suiteProofLedger is one gate run's ledger of owed and proved ground. It is
// process-wide because the gate is one process per run, and reset at each
// gate's entry so a long-lived caller (the test binary, above all) can never
// inherit a previous run's proof.
type suiteProofLedger struct {
	mu sync.Mutex
	// owed is the scope of each suite a stage decided this commit needs —
	// whether or not that stage then ran it.
	owed []runScope
	// unreadable records that a stage owed a scope this package cannot read
	// (a wrapper script, a runner the classifier does not know). Coverage
	// can then never be SHOWN, so it is never claimed: an unsubstantiated
	// claim is the defect, and silence costs only a note nobody gets.
	unreadable bool
	// unowned records that a staged file belongs to no [package] a cargo
	// root's ownership scan could find (classifyUnownedCargoFiles). There is
	// no runner to attach that file to as an owed scope — inventing one that
	// never runs would let some later stage mark it PROVED by mistake, which
	// is worse than the hole this flag closes — so it vetoes covered()
	// directly, the same as an unreadable owed scope: nothing tested that
	// file, so nothing may vouch for a tree that contains it.
	unowned bool
	// proved is the scope of each run that actually executed tests and passed.
	proved []runScope
}

var suiteProof suiteProofLedger

// gateSuiteProof is how code outside this file reaches the running gate's
// ledger: a pointer to the one value, never a copy (it holds its own mutex,
// and a copy would record into a second, separate ledger). A package above
// the one that owns the ledger gets the same one through it.
func gateSuiteProof() *suiteProofLedger { return &suiteProof }

// resetSuiteProof starts a fresh ledger, and clears suiteRanGreen with it:
// both describe what THIS gate ran, and a second gate in the same process
// must not stamp its tree on the first one's green. Called at the entry of
// every gate that can end in a claim.
func resetSuiteProof() {
	suiteRanGreen.Store(false)
	suiteProof.mu.Lock()
	defer suiteProof.mu.Unlock()
	suiteProof.owed, suiteProof.proved = nil, nil
	suiteProof.unreadable, suiteProof.unowned = false, false
}

// runnerScope is one Runner's width, in runscope.go's terms.
func runnerScope(r Runner) (runScope, bool) {
	return scopeOfSuiteCommand(append([]string{r.Cmd}, r.Args...))
}

// Owe records the ground this commit needs proven before anything may vouch
// for its tree.
func (l *suiteProofLedger) Owe(r Runner) {
	s, ok := runnerScope(r)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !ok {
		l.unreadable = true
		return
	}
	l.owed = append(l.owed, s)
}

// OweUnowned records that a staged file has no owning cargo package at all —
// see the unowned field's own comment for why that vetoes covered() rather
// than becoming an owed scope with a runner.
func (l *suiteProofLedger) OweUnowned() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unowned = true
}

// Note records a green run. A run that executed NO test (a build-only target,
// an empty selection) proves nothing and is not recorded, and neither is a
// command that is not a test invocation at all — `cargo clippy` and `go vet`
// share this stage's body, and a clean lint is not a passing suite. That last
// one is not hypothetical: in a Go repo the commit gate runs vet and lint and
// no suite whatsoever, so before this every commit here carried a `green`
// note minted by `go vet`.
func (l *suiteProofLedger) Note(r Runner, res SuiteResult) {
	if untestedVerdict(r, res) != "" {
		return
	}
	s, ok := runnerScope(r)
	if !ok {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.proved = append(l.proved, s)
}

// Covered reports whether every scope owed was proved. A run that owed
// nothing covers nothing: with no stage declaring what this commit needed,
// there is no ground to compare against and therefore nothing to claim.
func (l *suiteProofLedger) Covered() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unreadable || l.unowned || len(l.owed) == 0 {
		return false
	}
	for _, want := range l.owed {
		if !provenCovers(l.proved, want) {
			return false
		}
	}
	return true
}

// provenCovers reports whether the runs in have cover want. One run wide
// enough answers on its own; otherwise a multi-package scope is split and
// each package answered separately, since two runs of one crate each cover
// the same ground as one run of both.
func provenCovers(have []runScope, want runScope) bool {
	for _, h := range have {
		if scopeCovers(h, want) {
			return true
		}
	}
	if len(want.pkgs) < 2 {
		return false
	}
	for p := range want.pkgs {
		if !provenCovers(have, runScope{pkgs: map[string]bool{p: true}}) {
			return false
		}
	}
	return true
}

// reportSuitesNotRun names every touched scope whose suite this gate did NOT
// run. Issue #394's own remedy, and the reason it is a line rather than a
// refusal: the commit gate not running them is the design (see gateRoot), but
// an absence is invisible — the miss was only findable by reading every line
// of a long output and noticing which scope never appeared. A stage that
// stands down says so, on stderr and in gate.log. noun names what touched
// holds in THIS root's language ("crate" for cargo, "package" for go and
// everything narrowToStaged does not special-case) — a Go package is not a
// crate, and the wording must say so.
func reportSuitesNotRun(gateName, root, noun string, runner Runner, touched []string) {
	cmd := cmdString(runner)
	fmt.Fprintf(os.Stderr, "[mechanical] gate %s: %s in %s → NOT RUN — %s not tested here; a touched %s's suite runs at the merge gate, so this pass is not a green for it\n",
		gateName, cmd, root, strings.Join(touched, ", "), noun)
	AppendGateLog(gateName, root, cmd, "suites-not-run", 0)
}

// suiteNoun names a root's own suite scope in its own language, for
// reportSuitesNotRun's message.
func suiteNoun(cmd string) string {
	if cmd == "cargo" {
		return "crate"
	}
	return "package"
}

// suiteTouchedNames reads a scoped runner back into the names
// reportSuitesNotRun should print: the packages it names, sorted, or the
// runner's own command when the scope covers everything (nothing staged
// narrowed it) or is one scopeOfSuiteCommand cannot read at all (pytest,
// npm, zig — narrowToStaged has no related-tests mode for those, so their
// runner never carries a positional scope to read back).
func suiteTouchedNames(r Runner) []string {
	scope, ok := runnerScope(r)
	if !ok || scope.whole || len(scope.pkgs) == 0 {
		return []string{cmdString(r)}
	}
	names := make([]string, 0, len(scope.pkgs))
	for p := range scope.pkgs {
		names = append(names, p)
	}
	sort.Strings(names)
	return names
}

// --- the stamps ------------------------------------------------------------
//
// Two facts, deliberately two files, because they answer two questions with
// different burdens of proof. The GREEN stamp says a suite in this run went
// green on this tree; that is what the commit-msg claim check weighs a body's
// "verified" against (commitmsg_verify.go), and a guard crate's green is a
// fair answer to "did you run anything here". The PROVEN stamp says every
// scope this commit owed was covered, which is the stronger claim the git
// note makes to a reader who cannot see this box at all.

// provenSuiteStampFile holds the tree whose owed scopes were all proved.
func provenSuiteStampFile(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	return gcStatePath("proven-suite." + repoStateKey(repoRoot) + ".txt")
}

// stampProvenSuite records the staged tree as proven.
func stampProvenSuite(repoRoot string) {
	stampTree(provenSuiteStampFile(repoRoot), repoRoot)
}

// stampTree writes the tree the staged index would commit as into path.
func stampTree(path, repoRoot string) {
	tree := indexTree(repoRoot)
	if path == "" || tree == "" {
		return
	}
	_ = os.WriteFile(path, []byte(tree), 0o600)
}

// consumeProvenSuiteStamp reads and DELETES the proven stamp: it can vouch
// for one commit and no other.
func consumeProvenSuiteStamp(repoRoot string) string {
	path := provenSuiteStampFile(repoRoot)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
