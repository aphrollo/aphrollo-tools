package tdd

import (
	"io"
	"strings"
	"testing"
)

// borld#394: the commit gate passed a lane whose crates' test suites it never
// ran, and one of them was red.
//
// The pass itself is the design (TestPrecommit_RunsNoSuiteAtCommitTime): the
// touched crates' suites moved to the merge. What is NOT the design is what
// the gate then CLAIMED about that tree. Any green run in the gate set one
// process-wide flag, and the workspace's always-run GUARD crate — a pure
// crate that owns nothing the commit touched — is always one of them. So the
// post-commit hook wrote `green <tree>` on a commit whose touched crate was
// never compiled, and every reader downstream of that note (CI's escape
// trigger, the merge gate's own) took it as "the pre-commit gate proved this
// tree".
//
// The guard crate's green is a fact about the guard crate. It is not evidence
// about alpha, whose suite is red here and which nothing in this run ran.

// guardedWorkspace is a two-member cargo workspace declaring beta as its
// always-run guard crate, with a source change staged in alpha. The
// declaration is committed on its own first, for the same reason
// TestMechanical_CargoAlwaysRunPackage_RunsFirstAsItsOwnCommand does it: a
// staged root Cargo.toml opens a second rootGroup for the manifest.
func guardedWorkspace(t *testing.T) string {
	t.Helper()
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"beta\"]\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "declare always-run")
	write(t, root, "crates/alpha/src/lib.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")
	return root
}

// isSuiteVerb reports whether a runner is a TEST run rather than one of the
// fmt/clippy/check stages that share runSuiteStage's body.
func isSuiteVerb(r Runner) bool {
	if r.Cmd != "cargo" || len(r.Args) == 0 {
		return false
	}
	return r.Args[0] == "test" || (len(r.Args) > 1 && r.Args[0] == "nextest" && r.Args[1] == "run")
}

// redInAlpha fakes the borld#394 tree: alpha's suite is RED, every other run
// is green. It records the suite commands actually executed, so a test can
// say whether alpha's red was ever given the chance to speak.
func redInAlpha(ran *[]string) SuiteRunner {
	return func(r Runner, _ string) SuiteResult {
		if !isSuiteVerb(r) {
			return SuiteResult{Passed: true}
		}
		args := strings.Join(r.Args, " ")
		*ran = append(*ran, args)
		if strings.Contains(args, "-p alpha") {
			return SuiteResult{Passed: false, Output: "--- FAIL: alpha::pins_the_protocol_version\n"}
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 3 passed\n"}
	}
}

// noteAfterGate runs one gate over root, lets the commit path stamp whatever
// it proved, makes the commit and returns the gate note the post-commit hook
// wrote for it — "" when it wrote none. That note is the gate's claim as a
// machine reads it.
func noteAfterGate(t *testing.T, root string, gate func() GateResult) string {
	t.Helper()
	if res := gate(); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	StampGreenSuiteIfProven(root)
	gitDo(t, root, "commit", "-qm", "Widen alpha")
	PostCommit(root)
	return gitNote(t, root, "HEAD")
}

func TestPrecommit_ClaimsNoGreenForATouchedCrateItNeverTested(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := guardedWorkspace(t)

	var ran []string
	note := noteAfterGate(t, root, func() GateResult { return Precommit(root, redInAlpha(&ran)) })

	// The premise: alpha's suite never ran, so alpha's red is still out there.
	for _, c := range ran {
		if strings.Contains(c, "-p alpha") {
			t.Fatalf("premise broken — the commit gate ran alpha's suite (%q); this test is about the tree where it does not", c)
		}
	}
	if note != "" {
		t.Fatalf("the gate vouched for this tree (%q) off the guard crate's green alone: alpha was never compiled and its suite is red (suites run: %v)", note, ran)
	}
}

// The other half of the same claim, and issue #394's own named remedy: a
// crate whose suite this stage did not run must be NAMED as untested, so the
// absence of a suite line cannot be read as a pass by whoever reads the
// gate's output.
func TestPrecommit_NamesEachTouchedCrateWhoseSuiteItDidNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := guardedWorkspace(t)

	var ran []string
	out := captureStderr(t, func() {
		if res := Precommit(root, redInAlpha(&ran)); res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
	})
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "NOT RUN") {
		t.Fatalf("the gate never said alpha's suite went unrun; output:\n%s", out)
	}
}

// The positive control: the claim is not simply retired. A merge DOES run the
// touched crates' suites, and a green there is exactly the evidence the note
// is supposed to carry.
func TestMechanical_ClaimsTheGreenWhenTheTouchedCratesSuiteRan(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := guardedWorkspace(t)

	var ran []string
	green := func(r Runner, _ string) SuiteResult {
		if isSuiteVerb(r) {
			ran = append(ran, strings.Join(r.Args, " "))
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 3 passed\n"}
	}
	note := noteAfterGate(t, root, func() GateResult { return Mechanical(root, green) })

	var sawAlpha bool
	for _, c := range ran {
		if strings.Contains(c, "-p alpha") {
			sawAlpha = true
		}
	}
	if !sawAlpha {
		t.Fatalf("premise broken — the merge gate must run alpha's suite, ran %v", ran)
	}
	tree := gitOutT(t, root, "rev-parse", "HEAD:")
	if note != gateGreenNote(tree) {
		t.Fatalf("a run that tested every touched crate green must vouch for the tree: note = %q, want %q (suites run: %v)", note, gateGreenNote(tree), ran)
	}
}

// The same defect in this repo's own language, where it is starker still: a
// Go root's commit gate runs `go vet` and the linter and no suite whatsoever,
// so every commit here used to carry a note minted by a clean vet.
func TestPrecommit_ClaimsNoGreenForAGoPackageItOnlyVetted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var ran []Runner
	note := noteAfterGate(t, root, func() GateResult { return Precommit(root, recordRunner(&ran, root)) })

	if note != "" {
		t.Fatalf("the gate vouched for this tree (%q) without running a single test: runs %+v", note, ran)
	}
}

// reportSuitesNotRun's own remedy (see TestPrecommit_NamesEachTouchedCrateWhoseSuiteItDidNotRun,
// above) reached only gateRootCargo. gateRoot's non-cargo branch called
// suiteProof.owe for the same reason and then said nothing: a Go commit's
// standing-down suite left no line at all, so the absence this whole file
// exists to make visible was invisible again, one branch over.
func TestPrecommit_NamesTheGoPackageWhoseSuiteItDidNotRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var ran []Runner
	out := captureStderr(t, func() {
		if res := Precommit(root, recordRunner(&ran, root)); res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
	})
	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("the gate never said the go package's suite went unrun; output:\n%s", out)
	}
	if !strings.Contains(out, "internal/x") {
		t.Fatalf("the NOT RUN line never named the touched package; output:\n%s", out)
	}
	if strings.Contains(out, "crate") {
		t.Fatalf("a Go package is not a crate; output:\n%s", out)
	}
}

// And its positive control: the merge gate runs the staged package's tests,
// so the claim it leaves behind is one it can back.
func TestMechanical_ClaimsTheGreenForAGoPackageItTested(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var ran []Runner
	note := noteAfterGate(t, root, func() GateResult { return Mechanical(root, recordRunner(&ran, root)) })

	tree := gitOutT(t, root, "rev-parse", "HEAD:")
	if note != gateGreenNote(tree) {
		t.Fatalf("note = %q, want %q after a run that tested the staged package: runs %+v", note, gateGreenNote(tree), ran)
	}
}

// borld#309, #363, #384 and #449: four issues in eight days, all titled "the
// merge gate refused a lane whose pre-commit gate had run a suite green on the
// same tree". Read one by one, the merge gate was right every time — a
// 2006-line test file, a red replication-bandwidth budget, a test tree that
// did not compile, a red fuel test. What was wrong was the premise the
// recorder judged them by: the commit gate had NOT run those suites, it had
// run the guard crate, and the note it left behind said otherwise.
//
// So the trigger's own question — did two gates disagree about one tree? —
// only answers honestly if the note means what it says. End to end: a lane
// whose commit gate never tested its touched crate records nothing when the
// merge gate refuses it.
func TestMergeGate_RefusingALaneTheCommitGateNeverTestedRecordsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// The fixture repo has no GitHub remote, which is what keeps the recorder
	// from opening an issue if it decides to record one.
	root := guardedWorkspace(t)
	gitDo(t, root, "checkout", "-q", "-b", "lane")

	var ran []string
	if res := Precommit(root, redInAlpha(&ran)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	StampGreenSuiteIfProven(root)
	gitDo(t, root, "commit", "-qm", "Widen alpha")
	PostCommit(root)
	gitDo(t, root, "checkout", "-q", "-")
	t.Setenv(reflogActionEnv, "merge lane")

	NoteMergeGateEscape(root, "TDD mechanical: tests failing — fix before committing.\n"+
		"failing: alpha::pins_the_protocol_version\n", io.Discard)

	if n := len(readEscapes(t)); n != 0 {
		t.Fatalf("recorded %d escapes, want 0 — the merge gate ran alpha's suite for the first time and found it red, which is the gate working", n)
	}
}

// classifyUnownedCargoFiles drops a staged file no [package] owns from both
// owned and touched, on a stderr line alone — it never reaches suiteProof at
// all. So a commit that stages one owned crate (tested green here) alongside
// one such file reads as fully covered: every scope suiteProof knows about
// was proved, and the file nothing owns is invisible to it. This is #680's
// own failure mode, surviving on the one path #680 did not reach.
func TestMechanical_AnUnownedCargoFileLeavesTheTreeUnproven(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeMultiRootRepo(t)
	write(t, root, "crates/a/src/lib.rs", "pub fn base() -> i32 { 1 }\n")
	write(t, root, "misc.rs", "pub fn misc() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")

	var ran []string
	green := func(r Runner, _ string) SuiteResult {
		if isSuiteVerb(r) {
			ran = append(ran, strings.Join(r.Args, " "))
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed\n"}
	}
	note := noteAfterGate(t, root, func() GateResult { return Mechanical(root, green) })

	if note != "" {
		t.Fatalf("the gate vouched for this tree (%q) even though misc.rs has no owning cargo package and nothing tested it (suites run: %v)", note, ran)
	}
}

// suiteRanGreen is process-wide and was never reset: a second gate in the
// same process inherited the first one's "a suite ran green", and stamped
// its own tree green without running anything. Each gate entry point starts
// the flag afresh, as it starts the proof ledger.
func TestStampGreenSuite_ASecondGateInOneProcessDoesNotInheritTheFirstsGreen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	var ran []Runner
	if note := noteAfterGate(t, root, func() GateResult { return Mechanical(root, recordRunner(&ran, root)) }); note == "" {
		t.Fatalf("premise broken — the first gate ran no green suite; runs %+v", ran)
	}

	// The second gate: a commit gate over a Go source change, which runs no
	// suite at all.
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 2 }\n")
	gitDo(t, root, "add", ".")
	if res := Precommit(root, recordRunner(&ran, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	StampGreenSuiteIfProven(root)

	if stamped, ok := readCurrentGreenSuiteStamp(root); ok {
		t.Fatalf("the commit gate ran no suite, yet stamped tree %s green on the first gate's flag", stamped)
	}
}
