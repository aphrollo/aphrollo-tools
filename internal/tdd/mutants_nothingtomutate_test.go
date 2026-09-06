package tdd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A deletion has nothing left to generate a mutant from, and cargo-mutants
// says so on its own: "INFO Diff changes no Rust source files", then exits
// non-zero. That used to read as a death with no receipt at all
// (lane/crate-registry, issue #494) — a run that correctly found nothing to
// mutate is not a failure, and this pins that the wrapper around the
// producer now tells the two apart from the producer's own words rather than
// guessing at paths a second time. The lane's own diff carries a genuine
// deletion of src/lib.rs (Source-kind, not a manifest) so the path-based
// early waiver does NOT fire and the assertion below actually exercises the
// merge gate's receipt read, not the waiver.
func TestRunMutantsProducer_WritesAnHonestReceiptWhenTheToolFindsNothingToMutate(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	toolsDir := filepath.Join(root, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho ' INFO Diff changes no Rust source files'\nexit 2\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "mutation_gate.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "src", "lib.rs")); err != nil {
		t.Fatal(err)
	}
	tree := commitLane(t, root, "drop the placeholder crate, carry a producer that finds nothing")
	tip := gitOut(root, "rev-parse", "HEAD")
	diffPath := filepath.Join(t.TempDir(), "lane.diff")
	if err := os.WriteFile(diffPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	j := MutantsJob{
		Repo: "test-repo", RepoRoot: root, Branch: "lane/crate-registry",
		Tip: tip, TipTree: tree, BaseRef: "origin/main", BaseSHA: base,
		Worktree: root, TargetDir: filepath.Join(t.TempDir(), "target"),
		Diff: diffPath, Started: time.Now(),
	}

	code := runMutantsProducer(j, nil)
	if code != 0 {
		t.Fatalf("runMutantsProducer = %d, want 0 — a tool that correctly found nothing to mutate must exit clean, not be recorded as a death", code)
	}

	r, ok := readReceiptFile(MutationReceiptPathFor(j.TipTree))
	if !ok {
		t.Fatal("no receipt was written for a producer call that correctly measured zero mutants")
	}
	if r.Verdict != receiptVerdictPass {
		t.Fatalf("verdict = %q, want %q", r.Verdict, receiptVerdictPass)
	}
	if r.MutantsTotal != 0 {
		t.Fatalf("mutants_total = %d, want 0", r.MutantsTotal)
	}
	if r.ZeroReason != ReceiptZeroReasonNoMutableSource {
		t.Fatalf("zero_reason = %q, want %q — the merge gate needs to know THIS zero is explained", r.ZeroReason, ReceiptZeroReasonNoMutableSource)
	}
	if r.MAC == "" {
		t.Fatal("an honest zero-mutant receipt must be signed like any other, or it is refused as unsigned")
	}

	if got := checkMutationReceipt(receiptContext{RepoRoot: root, Repo: j.Repo, TipTree: tree, BaseSHA: base}); got != nil {
		t.Fatalf("a signed, explained zero-mutant receipt must merge: %s", got.Message)
	}
}

// A comment-only edit still names a REAL source file, unlike a deletion, so
// cargo-mutants runs normally, generates its mutants, finds none of them on
// the diff's one changed line, and reports "INFO No mutants to filter" on a
// perfectly ordinary exit 0 — the producer (borld's own tools/mutation_gate.sh
// included) already wrote and signed a receipt saying mutants_total: 0
// before this side ever sees the exit code (lane/testrig-edge, issue #494).
// This pins that the wrapper stamps the reason onto that EXISTING receipt
// rather than needing the producer itself to learn a new field.
func TestRunMutantsProducer_StampsAnExistingReceiptWhenTheToolFiltersOutEveryMutant(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, base := carryRepo(t)
	toolsDir := filepath.Join(root, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho ' INFO No mutants to filter'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "mutation_gate.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/lib.rs", "// points at the test that moved\npub fn base() -> i32 { 0 }\n")
	tree := commitLane(t, root, "point the doc comment at the file it actually moved to")
	tip := gitOut(root, "rev-parse", "HEAD")
	diffPath := filepath.Join(t.TempDir(), "lane.diff")
	if err := os.WriteFile(diffPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// What the real producer already wrote and signed BEFORE this side ever
	// sees its exit code: a perfectly ordinary zero-mutant pass, no
	// zero_reason, because the producer has not learned that field.
	pre := passingReceipt()
	pre.Repo, pre.TipTree, pre.BaseSHA = "test-repo", tree, base
	pre.MutantsTotal, pre.Caught = 0, 0
	writeReceipt(t, pre)

	j := MutantsJob{
		Repo: "test-repo", RepoRoot: root, Branch: "lane/testrig-edge",
		Tip: tip, TipTree: tree, BaseRef: "origin/main", BaseSHA: base,
		Worktree: root, TargetDir: filepath.Join(t.TempDir(), "target"),
		Diff: diffPath, Started: time.Now(),
	}

	code := runMutantsProducer(j, nil)
	if code != 0 {
		t.Fatalf("runMutantsProducer = %d, want 0 — the producer's own exit was already clean", code)
	}

	r, ok := readReceiptFile(MutationReceiptPathFor(tree))
	if !ok {
		t.Fatal("the producer's own receipt disappeared")
	}
	if r.ZeroReason != ReceiptZeroReasonNoMutableSource {
		t.Fatalf("zero_reason = %q, want %q — the stamp must land on the producer's own receipt", r.ZeroReason, ReceiptZeroReasonNoMutableSource)
	}
	if r.MAC == "" {
		t.Fatal("the stamped receipt must be re-signed, not left carrying the pre-stamp mac over a changed body")
	}

	if got := checkMutationReceipt(receiptContext{RepoRoot: root, Repo: "test-repo", TipTree: tree, BaseSHA: base}); got != nil {
		t.Fatalf("a stamped, explained zero-mutant receipt must merge: %s", got.Message)
	}
}

// A producer that exits non-zero for any OTHER reason — a real crash, a
// build failure, a test that failed under mutation — must still be recorded
// as a death. The nothing-to-mutate marker is the ONLY thing that turns a
// non-zero exit into a clean one; nothing else in the producer's output may.
func TestRunMutantsProducer_StillFailsWhenExitIsNonZeroWithNoNothingToMutateMarker(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	toolsDir := filepath.Join(root, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho 'error: the build failed'\nexit 2\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "mutation_gate.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "carry a producer script that genuinely fails")
	tip := gitOut(root, "rev-parse", "HEAD")
	tree := gitOut(root, "rev-parse", "HEAD:")
	diffPath := filepath.Join(t.TempDir(), "lane.diff")
	if err := os.WriteFile(diffPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	j := MutantsJob{
		Repo: "test-repo", RepoRoot: root, Branch: "lane/x",
		Tip: tip, TipTree: tree, BaseRef: "origin/main", BaseSHA: tip,
		Worktree: root, TargetDir: filepath.Join(t.TempDir(), "target"),
		Diff: diffPath, Started: time.Now(),
	}

	code := runMutantsProducer(j, nil)
	if code != 2 {
		t.Fatalf("runMutantsProducer = %d, want 2 — a genuine failure must not be swallowed into a clean exit", code)
	}
	if _, ok := readReceiptFile(MutationReceiptPathFor(j.TipTree)); ok {
		t.Fatal("a genuine producer failure must never write a passing receipt")
	}
}
