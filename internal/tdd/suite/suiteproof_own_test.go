package suite

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These are suite's own tests of suiteproof.go and autoescape_suite.go's
// indexTree, reached today only through internal/tdd/precommit's gate-note
// tests.

var (
	goPkgA  = Runner{Cmd: "go", Args: []string{"test", "./a"}}
	goPkgB  = Runner{Cmd: "go", Args: []string{"test", "./b"}}
	goWhole = Runner{Cmd: "go", Args: []string{"test", "./..."}}
	greenOK = SuiteResult{Passed: true, Output: "ok  \texample.com/m\t0.01s\n"}
)

// TestSuiteProofLedger_CoversWhenEveryOwedScopeWasProved pins the ledger's
// law on a private ledger: what a commit owes must each be proved by a green
// run at least as wide.
func TestSuiteProofLedger_CoversWhenEveryOwedScopeWasProved(t *testing.T) {
	t.Parallel()
	var l suiteProofLedger
	l.Owe(goPkgA)
	l.Owe(goPkgB)
	l.Note(goPkgA, greenOK)
	if l.Covered() {
		t.Fatal("only ./a was proved, ./b is still owed")
	}
	l.Note(goPkgB, greenOK)
	if !l.Covered() {
		t.Fatal("both owed packages were proved")
	}
}

// TestSuiteProofLedger_AWholeTreeRunProvesEveryPackage pins the width rule
// through the ledger.
func TestSuiteProofLedger_AWholeTreeRunProvesEveryPackage(t *testing.T) {
	t.Parallel()
	var l suiteProofLedger
	l.Owe(goPkgA)
	l.Owe(goPkgB)
	l.Note(goWhole, greenOK)
	if !l.Covered() {
		t.Fatal("a whole-tree run proves both packages")
	}
}

// TestSuiteProofLedger_NothingOwedClaimsNothing pins the empty ledger: with no
// stage having declared what the commit needed there is no ground to claim.
func TestSuiteProofLedger_NothingOwedClaimsNothing(t *testing.T) {
	t.Parallel()
	var l suiteProofLedger
	l.Note(goWhole, greenOK)
	if l.Covered() {
		t.Fatal("a ledger that owed nothing must not cover anything")
	}
}

// TestSuiteProofLedger_AnUnreadableOwedCommandVetoesTheClaim pins the fail-safe:
// a command whose scope cannot be read is owed but never provable.
func TestSuiteProofLedger_AnUnreadableOwedCommandVetoesTheClaim(t *testing.T) {
	t.Parallel()
	var l suiteProofLedger
	l.Owe(Runner{Cmd: "make"})
	l.Note(goWhole, greenOK)
	if l.Covered() {
		t.Fatal("an unreadable owed scope must veto the claim")
	}
}

// TestSuiteProofLedger_AnUnownedFileVetoesTheClaim pins OweUnowned: a staged
// file no crate owns is ground no stage will ever run.
func TestSuiteProofLedger_AnUnownedFileVetoesTheClaim(t *testing.T) {
	t.Parallel()
	var l suiteProofLedger
	l.Owe(goPkgA)
	l.Note(goPkgA, greenOK)
	l.OweUnowned()
	if l.Covered() {
		t.Fatal("an unowned staged file must veto the claim")
	}
}

// TestSuiteProofLedger_OnlyARunThatExecutedTestsIsRecorded pins Note's two
// guards: an empty selection proves nothing, and a command that is not a test
// invocation (a vet or a lint) is not a suite.
func TestSuiteProofLedger_OnlyARunThatExecutedTestsIsRecorded(t *testing.T) {
	t.Parallel()
	var l suiteProofLedger
	l.Owe(Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}})
	l.Note(Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}, SuiteResult{Passed: true, Output: libtestZero})
	l.Note(Runner{Cmd: "cargo", Args: []string{"clippy", "-p", "a"}}, SuiteResult{Passed: true})
	if l.Covered() {
		t.Fatal("an empty selection and a clippy run prove no suite")
	}
	l.Note(Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}, SuiteResult{Passed: true, Output: libtestPassed})
	if !l.Covered() {
		t.Fatal("a run that executed tests proves the owed package")
	}
}

// TestProvenCovers_ASplitOwedScopeMayBeProvedPackageByPackage pins the
// decomposition: an owed scope naming two packages is covered by two runs that
// each cover one, and not when one package is missing.
func TestProvenCovers_ASplitOwedScopeMayBeProvedPackageByPackage(t *testing.T) {
	t.Parallel()
	pk := func(names ...string) runScope {
		s := runScope{pkgs: map[string]bool{}}
		for _, n := range names {
			s.pkgs[n] = true
		}
		return s
	}
	if !provenCovers([]runScope{pk("a"), pk("b")}, pk("a", "b")) {
		t.Error("runs for a and b together cover an owed {a, b}")
	}
	if provenCovers([]runScope{pk("a")}, pk("a", "b")) {
		t.Error("a run for a alone does not cover an owed {a, b}")
	}
	if provenCovers(nil, pk("a")) {
		t.Error("no proved runs cover a single-package ask")
	}
}

// Serial: resets the process-wide suite-proof ledger and its ran-green flag.
// TestResetSuiteProof_ClearsTheLedgerAndTheRanGreenFlag pins that a fresh gate
// starts from nothing: what the last gate owed, proved and flagged is gone.
func TestResetSuiteProof_ClearsTheLedgerAndTheRanGreenFlag(t *testing.T) {
	l := gateSuiteProof()
	l.Owe(goPkgA)
	l.Note(goPkgA, greenOK)
	l.OweUnowned()
	suiteRanGreen.Store(true)

	resetSuiteProof()

	if suiteRanGreen.Load() {
		t.Error("suiteRanGreen must be cleared")
	}
	l.Owe(goPkgA)
	l.Note(goPkgA, greenOK)
	if !l.Covered() {
		t.Error("after a reset the ledger carries no leftover unowned/unreadable veto or stale owed scope")
	}
	resetSuiteProof()
}

// TestNotRunClause_NamesWhatWasNotTestedAndWhere pins the wording's parts.
func TestNotRunClause_NamesWhatWasNotTestedAndWhere(t *testing.T) {
	t.Parallel()
	got := notRunClause([]string{"alpha", "beta"}, "crate")
	for _, part := range []string{"NOT RUN", "alpha, beta", "a touched crate's suite", "not a green"} {
		if !strings.Contains(got, part) {
			t.Fatalf("clause %q lacks %q", got, part)
		}
	}
}

// TestSuiteNoun_IsCrateForCargoAndPackageForEverythingElse pins the noun.
func TestSuiteNoun_IsCrateForCargoAndPackageForEverythingElse(t *testing.T) {
	t.Parallel()
	if suiteNoun("cargo") != "crate" || suiteNoun("go") != "package" || suiteNoun("") != "package" {
		t.Fatal("cargo is a crate, anything else a package")
	}
}

// TestSuiteTouchedNames_ListsTheScopedPackagesSortedOrTheCommand pins the
// names: a scoped run lists its packages, a whole or unreadable one names its
// command.
func TestSuiteTouchedNames_ListsTheScopedPackagesSortedOrTheCommand(t *testing.T) {
	t.Parallel()
	got := suiteTouchedNames(Runner{Cmd: "go", Args: []string{"test", "./zed", "./alpha"}})
	if want := []string{"./alpha", "./zed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("scoped: %v, want %v", got, want)
	}
	if got := suiteTouchedNames(goWhole); !reflect.DeepEqual(got, []string{"go test ./..."}) {
		t.Errorf("whole: %v, want the command line", got)
	}
	if got := suiteTouchedNames(Runner{Cmd: "make", Args: []string{"check"}}); !reflect.DeepEqual(got, []string{"make check"}) {
		t.Errorf("unreadable: %v, want the command line", got)
	}
}

// Serial: appends to gate.log under its own CLAUDE_CONFIG_DIR, a process-wide env var.
// TestReportSuitesNotRun_LogsTheStandDown pins the log line: the verdict word
// the gate stats count.
func TestReportSuitesNotRun_LogsTheStandDown(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	reportSuitesNotRun("precommit", t.TempDir(), "package", goPkgA, []string{"./a"})
	requireLoggedVerdict(t, cfg, "suites-not-run")
}

// Serial: keeps its stamp under its own CLAUDE_CONFIG_DIR, a process-wide env var.
// TestProvenSuiteStamp_IsWrittenOnceAndConsumedOnce pins the stamp's life: the
// index tree is written for the repo, read back exactly once, and gone after.
func TestProvenSuiteStamp_IsWrittenOnceAndConsumedOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	want := indexTree(repo)
	if len(want) != 40 {
		t.Fatalf("indexTree = %q, want a 40-hex tree id", want)
	}

	stampProvenSuite(repo)
	if got := consumeProvenSuiteStamp(repo); got != want {
		t.Fatalf("consumed stamp = %q, want %q", got, want)
	}
	if got := consumeProvenSuiteStamp(repo); got != "" {
		t.Fatalf("a second consume = %q, want none: the stamp is single-use", got)
	}
}

// TestProvenSuiteStamp_NoRepoRootHasNoStampFile pins the empty-root arm.
func TestProvenSuiteStamp_NoRepoRootHasNoStampFile(t *testing.T) {
	t.Parallel()
	if got := provenSuiteStampFile(""); got != "" {
		t.Fatalf("stamp file for no repo = %q, want none", got)
	}
	if got := consumeProvenSuiteStamp(""); got != "" {
		t.Fatalf("consume for no repo = %q, want none", got)
	}
}

// TestStampTree_WritesNothingWithoutAPathOrATree pins the two guards: an empty
// path, and a root that is no repository (no tree to write).
func TestStampTree_WritesNothingWithoutAPathOrATree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stampTree("", dir)
	notARepo := filepath.Join(t.TempDir(), "stamp.txt")
	stampTree(notARepo, t.TempDir())
	if _, err := os.Stat(notARepo); err == nil {
		t.Fatal("a root that is no repository must leave no stamp")
	}
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestIndexTree_IsTheTreeTheStagedIndexWouldCommit pins the read: a staged
// change moves the tree id, and a directory that is no repository has none.
func TestIndexTree_IsTheTreeTheStagedIndexWouldCommit(t *testing.T) {
	repo := makeGoRepo(t)
	before := indexTree(repo)
	write(t, repo, "extra.go", "package m\n")
	gitDo(t, repo, "add", "extra.go")
	after := indexTree(repo)
	if before == "" || after == "" || before == after {
		t.Fatalf("index trees = %q then %q, want two different ids", before, after)
	}
	if got := indexTree(t.TempDir()); got != "" {
		t.Fatalf("indexTree outside a repository = %q, want none", got)
	}
}

// Serial: reads GIT_INDEX_FILE from the process-wide environment, and
// makeGoRepo sets git's config through it too.
// TestIndexTree_HonoursTheTemporaryIndexACommitBuilds pins the reason the call
// keeps GIT_INDEX_FILE: `git commit -a` and `git commit -- <paths>` build a
// temporary index, and the tree the gate stamps must be that index's, not
// .git/index's.
func TestIndexTree_HonoursTheTemporaryIndexACommitBuilds(t *testing.T) {
	repo := makeGoRepo(t)
	defaultTree := indexTree(repo)

	idx := filepath.Join(t.TempDir(), "tmp-index")
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(gitBinary(), args...)
		cmd.Dir = repo
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("read-tree", "HEAD")
	write(t, repo, "only_in_tmp_index.go", "package m\n")
	run("update-index", "--add", "only_in_tmp_index.go")

	t.Setenv("GIT_INDEX_FILE", idx)
	if got := indexTree(repo); got == "" || got == defaultTree {
		t.Fatalf("indexTree with a temporary index = %q, want a tree different from .git/index's %q", got, defaultTree)
	}
}
