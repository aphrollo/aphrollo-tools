package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// stageBrokenGoModule adds a go.mod plus a file that fails to COMPILE (not
// merely a failing test) to repo and stages both -- the same real mechanical
// block TestMechanical_BlocksARealCompileFailure (internal/tdd) proves,
// driven here through the cli dispatch instead of calling Mechanical
// directly.
func stageBrokenGoModule(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "broken.go"), []byte("package m\n\nfunc Broken() int { return }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := fixtureGit("-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
}

// TestRun_TDDPremergecommit_BlockedWritesMergeRejectedMarker pins the first
// half of the merge-abort-cleanup fix: when the pre-merge-commit hook
// actually rejects an automatic merge (Mechanical blocks on a real compile
// failure), the gate leaves a marker behind under the state dir so the
// git-queue shim can recognise ITS OWN rejection and clean up the
// MERGE_HEAD/index state a rejected automerge leaves in the shared checkout.
func TestRun_TDDPremergecommit_BlockedWritesMergeRejectedMarker(t *testing.T) {
	gateConfigDir(t)
	repo := commitRepo(t)
	stageBrokenGoModule(t, repo)

	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "premergecommit"}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("expected premergecommit to block a real compile failure, got exit 0\nstderr: %s", errb.String())
	}

	root := tdd.RepoRoot(repo)
	path := tdd.MergeRejectedMarkerPath(root)
	if path == "" {
		t.Fatal("MergeRejectedMarkerPath returned empty for a resolved repo root")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected a merge-rejected marker at %s, got: %v", path, err)
	}
}

// TestRun_TDDPrecommit_ConflictedMergeBlocked_WritesNoMarker pins the other
// half: concluding a real conflicted merge fires git's pre-commit hook, not
// pre-merge-commit (Precommit routes it to Mechanical internally, task A10),
// and that path must NEVER write the marker -- only a rejection reached
// through the premergecommit subcommand itself may. MERGE_HEAD is written
// directly (mergeInProgressRef only checks ref EXISTENCE via rev-parse
// --verify, mirrored from internal/tdd's own CHERRY_PICK_HEAD/REVERT_HEAD
// fixtures), which is cheaper than staging a real conflict for a test that
// only cares which subcommand ran.
func TestRun_TDDPrecommit_ConflictedMergeBlocked_WritesNoMarker(t *testing.T) {
	gateConfigDir(t)
	repo := commitRepo(t)

	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	mergeHead := append(bytes.TrimSpace(head), '\n')
	if err := os.WriteFile(filepath.Join(repo, ".git", "MERGE_HEAD"), mergeHead, 0o644); err != nil {
		t.Fatal(err)
	}
	stageBrokenGoModule(t, repo)

	var out, errb bytes.Buffer
	code := Run([]string{"tdd", "precommit"}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("expected precommit to block a real compile failure during a conflicted merge, got exit 0\nstderr: %s", errb.String())
	}

	root := tdd.RepoRoot(repo)
	path := tdd.MergeRejectedMarkerPath(root)
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("a conflicted-merge block must never write the automerge-rejected marker, but %s exists", path)
	}
}
