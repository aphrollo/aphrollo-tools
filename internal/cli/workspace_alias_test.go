package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

// TestWorkspaceUpdate_IsAnAliasOfRebase: `rebase` and `update` route through
// the identical function — proven by running both from OUTSIDE any git repo,
// where each hits the same cwd-resolution error, byte for byte.
func TestWorkspaceUpdate_IsAnAliasOfRebase(t *testing.T) {
	t.Chdir(t.TempDir()) // not a git repo

	var outU, errU bytes.Buffer
	codeU := runWorkspace([]string{"update"}, &outU, &errU)

	var outR, errR bytes.Buffer
	codeR := runWorkspace([]string{"rebase"}, &outR, &errR)

	if codeU != codeR {
		t.Fatalf("exit codes differ: update=%d rebase=%d", codeU, codeR)
	}
	if outU.String() != outR.String() || errU.String() != errR.String() {
		t.Errorf("update and rebase should hit the identical function:\nupdate stdout=%q stderr=%q\nrebase stdout=%q stderr=%q",
			outU.String(), errU.String(), outR.String(), errR.String())
	}
	if !strings.Contains(errU.String(), "not inside a git worktree") {
		t.Errorf("expected the cwd-resolution error, got stderr=%q", errU.String())
	}
}

// TestWorkspacePruneTicketForm_IsRemoveKeepBranch: `prune <repo> <branch>`
// routes through the identical function `remove <repo> <branch>
// --keep-branch` calls — proven by triggering each verb's standing-in-it
// guard (refuse to touch the worktree the caller is cd'd into) and checking
// the errors are byte-identical, which only holds if both went through
// RemovePlan's guard rather than PruneTicketPlan's differently-worded one.
func TestWorkspacePruneTicketForm_IsRemoveKeepBranch(t *testing.T) {
	isolateGit(t)
	repo := t.TempDir()
	gitInitRepo(t, repo)
	writeFile(t, filepath.Join(repo, "go.mod"), "module x\n\ngo 1.26\n")
	gitCommitAll(t, repo, "init")

	branch := "feat/x"
	slug, err := workspace.Slugify(branch)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workspace.DefaultWorktreeBase(repo), slug)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wt)

	var outRemove, errRemove bytes.Buffer
	codeRemove := runWorkspace([]string{"remove", repo, branch, "--keep-branch"}, &outRemove, &errRemove)

	var outPrune, errPrune bytes.Buffer
	codePrune := runWorkspace([]string{"prune", repo, branch}, &outPrune, &errPrune)

	if codeRemove != codePrune {
		t.Fatalf("exit codes differ: remove=%d prune=%d", codeRemove, codePrune)
	}
	if errRemove.String() != errPrune.String() {
		t.Errorf("prune's ticket form should hit the identical guard as remove --keep-branch:\nremove stderr=%q\nprune  stderr=%q",
			errRemove.String(), errPrune.String())
	}
	if !strings.Contains(errRemove.String(), "aphrollo workspace remove") {
		t.Errorf("expected RemovePlan's cwd-standing-in-it guard (names 'remove'), got: %q", errRemove.String())
	}
}

// TestWorkspaceVerify_PointsAtCheck: verify is a renamed verb now — it prints
// the rename notice as its first line and calls through the runCheckFn seam
// rather than running its own logic.
func TestWorkspaceVerify_PointsAtCheck(t *testing.T) {
	called := false
	prev := runCheckFn
	runCheckFn = func(args []string, stdout, stderr io.Writer) int {
		called = true
		return 0
	}
	t.Cleanup(func() { runCheckFn = prev })

	var out, errb bytes.Buffer
	code := runWorkspace([]string{"verify"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errb.String())
	}
	if !called {
		t.Error("verify should call through runCheckFn")
	}
	first, _, _ := strings.Cut(out.String(), "\n")
	if want := "workspace verify is now aphrollo check"; first != want {
		t.Errorf("first stdout line = %q, want %q", first, want)
	}
}
