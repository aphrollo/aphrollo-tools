package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The incident of issue #584: a repo's managed block still described a merge
// that needed a mutation receipt, and named two verbs that had been retired
// months earlier. Nothing in the tool ever compared the block a repo HAS with
// the block this build WOULD write, so a session inherited instructions for a
// gate that no longer exists and spent a lane on a failure that could not
// happen. Doctor judges it now.

// putManagedBlock writes a CLAUDE.md carrying body between the markers, with
// prose either side — the block never sits alone in a real repo, and the
// check has to find it inside a file it does not own.
func putManagedBlock(t *testing.T, repo, body string) {
	t.Helper()
	text := "# repo\n\nHouse rules of the repo itself.\n\n" + body + "\nA closing paragraph.\n"
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// currentBlock is what install would write into repo on this box.
func currentBlock(repo, shimDir string) string { return managedBlockFor(repo, shimDir) }

func TestDoctor_ReportsAManagedBlockWrittenByAnOlderTemplate(t *testing.T) {
	in := healthyInstall(t)
	stale := strings.Replace(currentBlock(in.Repo, in.ShimDir),
		"- **Housekeeping:**", "- **Housekeeping:** `aphrollo gate mutants watch` (background run) ·", 1)
	putManagedBlock(t, in.Repo, stale)

	c := check(t, Doctor(in), "CLAUDE.md block")
	if c.OK {
		t.Fatalf("a block from an older template must be a finding, got ok: %s", c.Detail)
	}
	for _, want := range []string{"stale", "Housekeeping", "aphrollo install"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", c.Detail, want)
		}
	}
	if strings.Contains(c.Detail, "queue shim") {
		t.Errorf("detail = %q, want the stale entry named, not the whole block quoted back", c.Detail)
	}
}

// The shape of #584 itself: the block carries a bullet the template dropped.
// Naming it is the whole finding — that bullet is what a session reads and
// obeys.
func TestDoctor_NamesAnEntryTheTemplateNoLongerCarries(t *testing.T) {
	in := healthyInstall(t)
	retired := "- **A merge needs a fresh receipt:** `aphrollo gate mutants watch` writes it.\n"
	block := strings.Replace(currentBlock(in.Repo, in.ShimDir), "\n_This block is written", retired+"\n_This block is written", 1)
	putManagedBlock(t, in.Repo, block)

	c := check(t, Doctor(in), "CLAUDE.md block")
	if c.OK {
		t.Fatalf("a retired entry must be a finding, got ok: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "A merge needs a fresh receipt") {
		t.Errorf("detail = %q, want it to name the retired entry", c.Detail)
	}
}

func TestDoctor_ABlockMatchingThisBuildIsNotAFinding(t *testing.T) {
	in := healthyInstall(t)
	putManagedBlock(t, in.Repo, currentBlock(in.Repo, in.ShimDir))

	c := check(t, Doctor(in), "CLAUDE.md block")
	if !c.OK || c.Warn {
		t.Fatalf("a current block must pass: ok=%v warn=%v detail=%q", c.OK, c.Warn, c.Detail)
	}
}

// A check that cries wolf is one nobody reads. Re-wrapped prose, CRLF line
// endings and trailing whitespace change the bytes and change nothing about
// what the block SAYS.
func TestDoctor_ARewrappedBlockWithTheSameContentIsNotAFinding(t *testing.T) {
	in := healthyInstall(t)
	block := currentBlock(in.Repo, in.ShimDir)
	rewrapped := strings.ReplaceAll(block, "\n  ", " ")
	rewrapped = strings.ReplaceAll(rewrapped, "\n", "   \r\n")
	if rewrapped == block {
		t.Fatal("the test rewrapped nothing")
	}
	putManagedBlock(t, in.Repo, rewrapped)

	c := check(t, Doctor(in), "CLAUDE.md block")
	if !c.OK {
		t.Fatalf("formatting alone must not be a finding: %s", c.Detail)
	}
}

// The queue dir is a fact about the BOX, not about the template's currency:
// a repo whose block was written on another machine (or judged on a CI
// runner) says a different path and is not stale for it.
func TestDoctor_AQueueDirFromAnotherBoxIsNotAFinding(t *testing.T) {
	in := healthyInstall(t)
	block := strings.Replace(currentBlock(in.Repo, in.ShimDir),
		"`"+shellPath(in.ShimDir)+"`", "`/usr/local/bin/cargo-queue`", 1)
	if block == currentBlock(in.Repo, in.ShimDir) {
		t.Fatal("the test replaced no queue dir")
	}
	putManagedBlock(t, in.Repo, block)

	c := check(t, Doctor(in), "CLAUDE.md block")
	if !c.OK {
		t.Fatalf("another box's queue dir must not read as stale: %s", c.Detail)
	}
}

// A repo that never opted in has nothing to be behind: no CLAUDE.md at all,
// or one with no managed block, reports no line rather than a passing one —
// silence is what "does not apply" looks like on this report.
func TestDoctor_ARepoWithNoManagedBlockReportsNoLine(t *testing.T) {
	in := healthyInstall(t)
	for _, name := range checkNames(Doctor(in)) {
		if name == "CLAUDE.md block" {
			t.Fatalf("a repo with no CLAUDE.md must report no block line")
		}
	}

	if err := os.WriteFile(filepath.Join(in.Repo, "CLAUDE.md"), []byte("# repo\n\nhouse rules, no aphrollo block\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range checkNames(Doctor(in)) {
		if name == "CLAUDE.md block" {
			t.Fatalf("a CLAUDE.md with no managed block must report no block line")
		}
	}
}

// The merge-only primary cannot take the write that fixes it (the git shim
// refuses the commit), so telling the operator to run install THERE is advice
// that cannot be followed. The remedy is a lane, which is what install itself
// says in the same situation.
func TestDoctor_AStaleBlockInAMergeOnlyPrimaryPointsAtALane(t *testing.T) {
	in := healthyInstall(t)
	root := makeGoRepo(t)
	gitDo(t, root, "branch", "-M", "main")
	addWorktree(t, root, "lane-a")
	in.Repo = root
	putManagedBlock(t, root, strings.Replace(currentBlock(root, in.ShimDir),
		"- **Housekeeping:**", "- **Housekeeping:** stale text ·", 1))

	c := check(t, Doctor(in), "CLAUDE.md block")
	if c.OK {
		t.Fatalf("a stale block is a finding in the primary too, got ok: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "lane") {
		t.Errorf("detail = %q, want the remedy to be a lane, not an install the primary refuses", c.Detail)
	}
}
