package tdd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// carryRepo is a committed repo with one Rust file, returning the repo root
// and the sha of the commit before the lane's work — the merge base every
// test here judges against.
func carryRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = makeCargoRepo(t)
	base = gitOut(root, "rev-parse", "HEAD")
	return root, base
}

// commitLane stages everything and commits, returning the new tree hash.
func commitLane(t *testing.T, root, msg string) string {
	t.Helper()
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", msg)
	tree := gitOut(root, "rev-parse", "HEAD:")
	if tree == "" {
		t.Fatal("no tree for HEAD")
	}
	return tree
}

// readReceipt decodes the receipt on disk for one tree.
func readReceipt(t *testing.T, tree string) MutationReceipt {
	t.Helper()
	data, err := os.ReadFile(MutationReceiptPathFor(tree))
	if err != nil {
		t.Fatal(err)
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func laneContext(root, tree, base string) receiptContext {
	return receiptContext{RepoRoot: root, Repo: "borld", TipTree: tree, BaseSHA: base}
}

// A lane that changed no Source and no Test file has nothing to mutate, so
// demanding a receipt for it only buys a four-minute workspace build that
// writes an empty answer. A workflow-only lane was refused for exactly that
// while a Markdown-only lane passed (issue #102).
func TestMutationReceipt_NotRequiredWhenTheLaneChangesNoSourceOrTestFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	write(t, root, ".github/workflows/ci.yml", "name: ci\n")
	write(t, root, "README.md", "docs\n")
	tree := commitLane(t, root, "ci: pin the runner")

	if got := checkMutationReceipt(laneContext(root, tree, base)); got != nil {
		t.Fatalf("a lane with nothing mutable must merge without a receipt: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-not-required:"+short(tree))
}

// A lane whose entire diff is Test-kind files has no mutable scope either:
// cargo-mutants only ever generates a mutant from a package's own SOURCE, so
// a diff that moves or edits only tests/*.rs can never produce one. Refusing
// its honestly-zero receipt sent the author to "check the merge base" for a
// base that was never wrong (issue #488).
func TestMutationReceipt_NotRequiredWhenTheLaneChangesOnlyTestFiles(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	write(t, root, "tests/moved.rs", "#[test]\nfn it_works() {}\n")
	tree := commitLane(t, root, "move an integration test into this crate")

	if got := checkMutationReceipt(laneContext(root, tree, base)); got != nil {
		t.Fatalf("a lane touching only test files must merge without a receipt: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-not-required:"+short(tree))
}

// A lane whose entire diff is manifest files has no mutable scope either,
// for the same reason as a Test-only diff: cargo-mutants generates a mutant
// from a package's own source, and a Cargo.toml/Cargo.lock edit holds no
// function body to mutate. ClassifyFile answers Source for a manifest ON
// PURPOSE (a dependency bump must still run the suite, issue #278), so the
// naive "no Source path" reading of laneHasNothingToMutate would still
// refuse this diff — a second lane hit exactly that (three Cargo.toml edits
// plus Cargo.lock, no .rs file touched) under issue #488.
func TestMutationReceipt_NotRequiredWhenTheLaneChangesOnlyManifestFiles(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n\n[dependencies]\n")
	write(t, root, "Cargo.lock", "# lockfile placeholder\n")
	tree := commitLane(t, root, "drop an unused dependency")

	if got := checkMutationReceipt(laneContext(root, tree, base)); got != nil {
		t.Fatalf("a lane touching only manifest files must merge without a receipt: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-not-required:"+short(tree))
}

// A real lane arrives with Test, manifest and Ignore paths together, never
// one kind alone — the isolated cases above are the artificial ones. The
// reported case, lane/crate-registry, deletes an empty placeholder crate and
// adds a guard test: its diff against main was one Test file, three
// manifests, a law and several docs, and nothing Source that was not itself
// a manifest. This sits between the Test-only and manifest-only cases and
// fails if either half of isMutableSourcePath's predicate is dropped: a
// manifest edit here counting as mutable source (isMutableSourcePath
// narrowed to bare Source) or the guard test itself somehow counting as one.
func TestMutationReceipt_NotRequiredWhenTheLaneMixesTestManifestAndIgnorePaths(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	write(t, root, "tests/guard.rs", "#[test]\nfn it_works() {}\n")
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n\n[dependencies]\n")
	write(t, root, "Cargo.lock", "# lockfile placeholder\n")
	write(t, root, "docs/notes.md", "explain the removal\n")
	tree := commitLane(t, root, "drop the placeholder crate and add a guard test")

	if got := checkMutationReceipt(laneContext(root, tree, base)); got != nil {
		t.Fatalf("a lane mixing Test, manifest and Ignore paths must merge without a receipt: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-not-required:"+short(tree))
}

// A Test-kind diff earns the early not-required exemption directly; it must
// never fall through to the vacuous-receipt check and be refused for the
// zero counts that diff can only ever produce. A receipt existing at all
// here (with the shape issue #488 describes: zero mutants, zero moved
// lines, a passing verdict) still must not be read — the lane needed none.
func TestMutationReceipt_TestOnlyDiffNeverReachesTheVacuousCheck(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	write(t, root, "tests/moved.rs", "#[test]\nfn it_works() {}\n")
	tree := commitLane(t, root, "move an integration test into this crate")

	r := passingReceipt()
	r.TipTree, r.BaseSHA = tree, base
	r.MutantsTotal, r.MovedLines = 0, 0
	writeReceipt(t, r)

	if got := checkMutationReceipt(laneContext(root, tree, base)); got != nil {
		t.Fatalf("a test-only diff must be waived before the vacuous check ever runs: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-not-required:"+short(tree))
}

// One .rs in the diff is the whole reason the gate exists.
func TestMutationReceipt_StillRequiredWhenTheDiffCarriesOneSourceFile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := carryRepo(t)
	write(t, root, ".github/workflows/ci.yml", "name: ci\n")
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	tree := commitLane(t, root, "add a crate file beside the workflow")

	got := checkMutationReceipt(laneContext(root, tree, base))
	if got == nil || !got.Blocked {
		t.Fatal("a lane carrying a source file must still need a receipt")
	}
}

// The measured run is still the truth about the code being merged when the
// only thing that changed since is a document: a docs-only fix commit must
// not cost a second multi-hour run.
func TestMutationReceipt_CarriesForwardOverADocsOnlyCommit(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	measured := commitLane(t, root, "add the code the run measured")

	r := passingReceipt()
	r.TipTree, r.BaseSHA = measured, base
	r.FinishedAt = time.Now().UTC()
	writeReceipt(t, r)

	write(t, root, "docs/notes.md", "explain it\n")
	tip := commitLane(t, root, "docs: explain it")

	if got := checkMutationReceipt(laneContext(root, tip, base)); got != nil {
		t.Fatalf("a docs-only commit must carry the run forward: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-carried:"+short(measured)+"->"+short(tip))
	// Re-stamped on disk under the tree it now describes, so the next lookup
	// is a plain hit and the carry is auditable.
	if _, err := os.Stat(MutationReceiptPathFor(tip)); err != nil {
		t.Fatalf("the carried receipt was not written for the new tree: %v", err)
	}
	carried := readReceipt(t, tip)
	if carried.CarriedFrom != measured {
		t.Fatalf("carried_from = %q, want the tree it was measured on", carried.CarriedFrom)
	}
}

// One .rs blob differing is a different program, whatever else the commit
// touched: the run measured code that is no longer being merged.
func TestMutationReceipt_RefusesToCarryForwardWhenOneSourceBlobDiffers(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := carryRepo(t)
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	measured := commitLane(t, root, "add the code the run measured")

	r := passingReceipt()
	r.TipTree, r.BaseSHA = measured, base
	writeReceipt(t, r)

	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 3 }\n")
	write(t, root, "docs/notes.md", "explain it\n")
	tip := commitLane(t, root, "change the code and the docs")

	got := checkMutationReceipt(laneContext(root, tip, base))
	if got == nil || !got.Blocked {
		t.Fatal("a changed source blob must not ride another tree's receipt")
	}
	if !strings.Contains(got.Message, "mutation receipt missing for tree "+short(tip)) {
		t.Fatalf("message = %q, want the one-line remedy naming the tree", got.Message)
	}
}

// A receipt measured against ANOTHER base mutated another diff, so it may not
// be carried onto this merge either — the carry rule is narrower than the
// base rule, never a way around it.
func TestMutationReceipt_RefusesToCarryForwardFromAnotherBase(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base := carryRepo(t)
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	measured := commitLane(t, root, "add the code the run measured")

	r := passingReceipt()
	r.TipTree, r.BaseSHA = measured, otherBase
	writeReceipt(t, r)

	write(t, root, "docs/notes.md", "explain it\n")
	tip := commitLane(t, root, "docs: explain it")

	got := checkMutationReceipt(laneContext(root, tip, base))
	if got == nil || !got.Blocked {
		t.Fatal("a receipt from another base must not be carried onto this merge")
	}
}

// The one-line remedy is the whole rejection when there is simply no proof:
// a paragraph restating what a receipt is buys nothing at the moment a merge
// needs one command.
func TestMutationReceipt_MissingReceiptRejectsWithOneRemedyLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("no receipt must not merge")
	}
	if lines := strings.Count(strings.TrimSpace(got.Message), "\n"); lines != 0 {
		t.Fatalf("message spans %d extra lines, want exactly one:\n%s", lines, got.Message)
	}
	want := "gate: mutation receipt missing for tree " + short(laneTip) + " — run `aphrollo gate mutants run` in the lane"
	if got.Message != want {
		t.Fatalf("message = %q, want %q", got.Message, want)
	}
}

// The remedy must never claim a script the repo does not have: with no
// RepoRoot to check at all, it falls back to this binary's own runner rather
// than guessing tools/mutation_gate.sh — the bug issue #117 evidenced (a
// scratch Go repo with no such script was told to run it anyway).
func TestMutationReceipt_MissingReceiptNeverNamesAScriptTheRepoLacks(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)

	got := checkMutationReceipt(receiptContext{RepoRoot: root, Repo: "go-repo", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("no receipt must not merge")
	}
	want := "gate: mutation receipt missing for tree " + short(laneTip) + " — run `aphrollo gate mutants run` in the lane"
	if got.Message != want {
		t.Fatalf("message = %q, want %q (this repo has no tools/mutation_gate.sh)", got.Message, want)
	}
}

// A repo that actually carries the script has it NAMED, as what the run will
// drive. It is not offered as the command to type: invoked by hand it runs
// outside the box-wide lock and in the wrong tree, which is how one box ended
// up with several cold builds fighting for the same cores.
func TestMutationReceipt_MissingReceiptNamesTheScriptWhenTheRepoHasOne(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "tools/mutation_gate.sh", "#!/bin/sh\n")

	got := checkMutationReceipt(receiptContext{RepoRoot: root, Repo: "cargo-repo", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("no receipt must not merge")
	}
	want := "gate: mutation receipt missing for tree " + short(laneTip) + " — run `aphrollo gate mutants run` in the lane — it drives tools/mutation_gate.sh for you, under the box-wide lock"
	if got.Message != want {
		t.Fatalf("message = %q, want %q", got.Message, want)
	}
}

// A Cargo workspace that declares the key but leaves it EMPTY has not
// actually named a runner — falling through to the next source is what
// tells "declared and blank" apart from "declared and real".
func TestMutantsRunnerCommand_FallsThroughAnEmptyCargoDeclaredName(t *testing.T) {
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[workspace]\n\n[workspace.metadata.aphrollo]\nmutation-runner = \"\"\n")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-runner = \"tools/real.sh\"\n")

	if got := mutantsRunnerCommand(root); got != "tools/real.sh" {
		t.Fatalf("mutantsRunnerCommand = %q, want the aphrollo.toml name once the Cargo one is empty", got)
	}
}

// A repo that declares its own runner is named by exactly that — a
// workspace's own choice outranks either default.
func TestMutationReceipt_MissingReceiptNamesTheDeclaredRunner(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-runner = \"tools/mutants.sh\"\n")

	got := checkMutationReceipt(receiptContext{RepoRoot: root, Repo: "go-repo", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("no receipt must not merge")
	}
	want := "gate: mutation receipt missing for tree " + short(laneTip) + " — run `aphrollo gate mutants run` in the lane — it drives tools/mutants.sh for you, under the box-wide lock"
	if got.Message != want {
		t.Fatalf("message = %q, want %q", got.Message, want)
	}
}
