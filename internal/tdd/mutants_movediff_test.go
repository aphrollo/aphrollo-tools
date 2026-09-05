package tdd

import (
	"slices"
	"strings"
	"testing"
)

// A crate-topology lane MOVES code: a module leaves one crate and arrives in
// another, byte for byte. To `--in-diff` every one of those lines is a changed
// line, so a split lane would mutate thousands of lines whose behaviour nobody
// touched — hours of measurement for a diff that says nothing.
//
// movedRepo builds the real thing rather than a hand-written diff: git's own
// move detection is what decides here, and a fixture diff would be testing the
// fixture.
func movedRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = makeCargoRepo(t)
	write(t, root, "src/a.rs", strings.Join([]string{
		"pub fn keep(x: i32) -> i32 {",
		"    x + 1",
		"}",
		"",
		"pub fn moving(x: i32) -> i32 {",
		"    if x > 3 {",
		"        return x * 2;",
		"    }",
		"    x - 1",
		"}",
	}, "\n")+"\n")
	write(t, root, "src/b.rs", "pub fn other() -> i32 { 7 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "base")
	sha, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base = sha
	gitDo(t, root, "checkout", "-q", "-b", "lane/move")
	return root, strings.TrimSpace(base)
}

// A pure move: the same ten lines, in another file, unchanged.
func moveTheFunction(t *testing.T, root string, extra string) {
	t.Helper()
	write(t, root, "src/a.rs", strings.Join([]string{
		"pub fn keep(x: i32) -> i32 {",
		"    x + 1",
		"}",
	}, "\n")+"\n")
	body := "        return x * 2;"
	if extra != "" {
		body = extra
	}
	write(t, root, "src/b.rs", strings.Join([]string{
		"pub fn other() -> i32 { 7 }",
		"",
		"pub fn moving(x: i32) -> i32 {",
		"    if x > 3 {",
		body,
		"    }",
		"    x - 1",
		"}",
	}, "\n")+"\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "move it")
}

// Nothing in a pure move is worth mutating: every changed line is the same
// line somewhere else.
func TestLaneDiff_APureMoveBetweenFilesLeavesNoLinesToMutate(t *testing.T) {
	root, base := movedRepo(t)
	moveTheFunction(t, root, "")

	diff, moved := moveAwareDiff(root, base, "HEAD", []string{"src/a.rs", "src/b.rs"})
	if moved == 0 {
		t.Fatal("git detected no moved lines at all, so this test is not exercising the filter")
	}
	if hunks := countDiffHunks(diff); hunks != 0 {
		t.Fatalf("%d hunk(s) survived a pure move:\n%s", hunks, diff)
	}
}

// The one line that really changed is the one line that gets mutated. This is
// the half that keeps the filter honest: dropping moved lines must not drop
// the edit that rode along with them.
func TestLaneDiff_AMoveWithOneEditedLineKeepsOnlyThatLine(t *testing.T) {
	root, base := movedRepo(t)
	moveTheFunction(t, root, "        return x * 3;")

	diff, _ := moveAwareDiff(root, base, "HEAD", []string{"src/a.rs", "src/b.rs"})
	added := diffAddedLines(diff)
	if len(added) != 1 {
		t.Fatalf("kept %d added line(s), want only the edited one:\n%s\nlines: %q", len(added), diff, added)
	}
	if !strings.Contains(added[0], "x * 3") {
		t.Fatalf("kept line = %q, want the line that actually changed", added[0])
	}
}

// A lane that is nothing but moves gets a receipt saying so, rather than no
// receipt (which blocks the merge) or a measured one (which costs hours).
func TestRunMutantsJob_ALaneOfPureMovesGetsAZeroMutantReceipt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, _ := movedRepo(t)
	// The mutation-receipt opt-in is the repo's OWN standing configuration,
	// not part of the lane under test — folded into master (re-branching
	// lane/move from the result) so the lane's own diff stays a pure move.
	// Landing it as a commit ON the lane used to work only because Cargo.toml
	// classified as Ignore; once #278 made a manifest edit real Source (and
	// so mutation-relevant), that same commit was a genuine, non-moved edit
	// sitting in the lane's own diff, and the producer ran for a lane the
	// test's whole premise says moved code only.
	gitDo(t, root, "checkout", "-q", "master")
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "opt in")
	gitDo(t, root, "checkout", "-q", "-B", "lane/move")
	moveTheFunction(t, root, "")
	withFreeSpace(t, 200)

	j := laneJob(t, root)
	var ran []MutantsJob
	recordProducer(t, &ran)
	RunMutantsJob(writeJobFile(t, j))

	if len(ran) != 0 {
		t.Fatalf("a producer ran for a lane that only moved code: %+v", ran)
	}
	r, ok := readReceiptFile(MutationReceiptPathFor(j.TipTree))
	if !ok {
		t.Fatal("no receipt, so a pure-move lane cannot merge at all")
	}
	if r.MutantsTotal != 0 || r.Verdict != receiptVerdictPass {
		t.Fatalf("receipt = total %d verdict %q, want 0 and pass", r.MutantsTotal, r.Verdict)
	}
	if r.MovedLines == 0 {
		t.Fatal("the receipt does not record how many lines were moved, so nothing says why it measured nothing")
	}
	if r.MAC == "" {
		t.Fatal("a zero-mutant receipt must be signed like any other")
	}
}

// A pure move across files leaves neither file with anything left to
// mutate: git's move detection empties both diffs entirely, so both are
// reported for exclusion from the run, and the moved-line count is the
// evidence a zero-mutant receipt can point to.
func TestMovedOnlyFiles_APureMoveExcludesBothFilesAndCountsTheLines(t *testing.T) {
	root, base := movedRepo(t)
	moveTheFunction(t, root, "")

	moved, movedLines := movedOnlyFiles(root, base, "HEAD", []string{"src/a.rs", "src/b.rs"})
	if movedLines == 0 {
		t.Fatal("git detected no moved lines at all, so this test is not exercising the filter")
	}
	if len(moved) != 2 || !slices.Contains(moved, "src/a.rs") || !slices.Contains(moved, "src/b.rs") {
		t.Fatalf("moved = %v, want both src/a.rs and src/b.rs — neither has anything left to mutate", moved)
	}
}

// The one line that really changed rides along with the move, and the file
// that carries it must stay IN the run — reporting it moved-only would let a
// real edit go unmeasured.
func TestMovedOnlyFiles_AMoveWithOneEditedLineKeepsThatFileInTheRun(t *testing.T) {
	root, base := movedRepo(t)
	moveTheFunction(t, root, "        return x * 3;")

	moved, _ := movedOnlyFiles(root, base, "HEAD", []string{"src/a.rs", "src/b.rs"})
	if slices.Contains(moved, "src/b.rs") {
		t.Fatalf("moved = %v, want src/b.rs kept — it carries the one line that actually changed", moved)
	}
}

// countDiffHunks counts the hunks a diff still carries.
func countDiffHunks(diff string) int {
	n := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "@@") {
			n++
		}
	}
	return n
}

// diffAddedLines is every line the diff still adds.
func diffAddedLines(diff string) []string {
	var out []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			out = append(out, line)
		}
	}
	return out
}
