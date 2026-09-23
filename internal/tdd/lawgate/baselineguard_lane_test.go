package lawgate

import (
	"path/filepath"
	"strings"
	"testing"
)

// The guard compared a staged baseline against HEAD — the lane's own previous
// commit. A raise landed by an earlier commit in the lane is therefore already
// in HEAD, so every later commit compared it against itself and found nothing,
// while the merge gate compares the lane against MAIN and sees it:
//
//	gate premergecommit: ratchet → REJECTED
//	  module_size: internal/ratchet/check.go 831 lines (max 600) (baseline 781, now 831)
//
// after a pre-commit gate that had run green on the same tree (escape #206).
// A baseline only ever goes down relative to what the lane will merge INTO,
// which is the base branch, so that is what the comparison is against.
func TestBaselineGuard_SeesARaiseAnEarlierCommitInTheLaneAlreadyLanded(t *testing.T) {
	const path = "crates/ratchet/tests/module_size_baseline.txt"
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, path), "# header\ncrates/a.rs | 1048\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitDo(t, root, "branch", "-f", "main")
	gitDo(t, root, "checkout", "-q", "-b", "lane/raise")

	// The lane's first commit raises the ceiling.
	mustWrite(t, filepath.Join(root, path), "# header\ncrates/a.rs | 1049\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// The commit being gated touches something else entirely.
	mustWrite(t, filepath.Join(root, "notes.txt"), "unrelated\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)

	if !res.Blocked {
		t.Fatal("the guard passed a lane carrying a raised ceiling, so the merge gate is the first thing to see it — which is exactly the escape")
	}
	for _, want := range []string{"crates/a.rs", "1048 -> 1049"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

// ...and the widening stops at the lane's base: a ceiling that was already
// where it is on main is not this lane's offence, and blocking on it would
// make every commit in the repo unpassable.
func TestBaselineGuard_DoesNotChargeTheLaneForACeilingItsBaseAlreadyCarried(t *testing.T) {
	const path = "crates/ratchet/tests/module_size_baseline.txt"
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, path), "# header\ncrates/a.rs | 1049\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitDo(t, root, "branch", "-f", "main")
	gitDo(t, root, "checkout", "-q", "-b", "lane/tidy")

	mustWrite(t, filepath.Join(root, "notes.txt"), "unrelated\n")
	gitAddAll(t, root)

	if res := baselineStage("precommit", root); res.Blocked {
		t.Errorf("the guard charged the lane for its base's own ceiling: %q", res.Message)
	}
}

// The staged raise stays rejected on the commit that makes it: the widening
// adds a case, it does not move one.
func TestBaselineGuard_StillRejectsARaiseStagedInThisCommit(t *testing.T) {
	root := baselineRepo(t, "crates/ratchet/tests/module_size_baseline.txt",
		"# header\ncrates/a.rs | 1048\n",
		"# header\ncrates/a.rs | 1049\n")

	if res := baselineStage("precommit", root); !res.Blocked {
		t.Fatal("a hand-raised ceiling staged in this very commit must still reject it")
	}
}
