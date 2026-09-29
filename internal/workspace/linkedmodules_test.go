package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// ignoreNodeModules makes node_modules an ignored path in repo and all its
// worktrees, as a node repo's .gitignore does: git removes an ignored tree
// with the worktree, which is how it reaches a linked one.
func ignoreNodeModules(t *testing.T, repo string) {
	t.Helper()
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("node_modules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// installedPackage writes node_modules/fakepkg/index.js under dir and returns
// the package file.
func installedPackage(t *testing.T, dir string) string {
	t.Helper()
	file := filepath.Join(dir, depinstall.NodeModules, "fakepkg", "index.js")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// linkedLane prepares a lane whose node_modules links the primary checkout's
// installed one — a symlink here, the shape a lane carries on Unix — and
// returns the primary's package file.
func linkedLane(t *testing.T) (repo, wt, branch, primaryFile string) {
	t.Helper()
	repo, wt, branch = preparedRepo(t)
	ignoreNodeModules(t, repo)
	primaryFile = installedPackage(t, repo)
	if err := os.Symlink(filepath.Join(repo, depinstall.NodeModules), filepath.Join(wt, depinstall.NodeModules)); err != nil {
		t.Fatal(err)
	}
	return repo, wt, branch, primaryFile
}

// junctionLane prepares a lane holding a node_modules RemoveLinks sees as a
// Windows junction (the seam) and returns the package file reached through
// it. The stand-in is a real directory, so a removal that walks into it or
// lets git delete it is caught by that file going missing.
func junctionLane(t *testing.T) (repo, wt, branch, throughFile string) {
	t.Helper()
	repo, wt, branch = preparedRepo(t)
	ignoreNodeModules(t, repo)
	t.Cleanup(depinstall.TreatAsJunction(depinstall.NodeModules))
	return repo, wt, branch, installedPackage(t, wt)
}

func mustExist(t *testing.T, file, what string) {
	t.Helper()
	if _, err := os.Stat(file); err != nil {
		t.Errorf("%s must keep its contents after the removal: %v", what, err)
	}
}

// #947: `workspace remove` must unlink a lane's linked node_modules and never
// delete through it into the primary checkout's install.
func TestRemove_NeverDeletesThroughALinkedNodeModules(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		repo, wt, branch, primary := linkedLane(t)
		r, err := RemovePlan(repo, branch, "")
		if err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if err := r.Run(&out, &errb); err != nil {
			t.Fatalf("Run: %v\n%s", err, errb.String())
		}
		if _, err := os.Lstat(wt); !os.IsNotExist(err) {
			t.Errorf("the lane must be gone, lstat err = %v", err)
		}
		mustExist(t, primary, "the primary's node_modules")
	})
	t.Run("junction", func(t *testing.T) {
		repo, _, branch, through := junctionLane(t)
		r, err := RemovePlan(repo, branch, "")
		if err != nil {
			t.Fatal(err)
		}
		r.Force = true
		var out, errb bytes.Buffer
		if err := r.Run(&out, &errb); err == nil {
			t.Error("a junction that could not be unlinked must stop the removal")
		}
		mustExist(t, through, "the junction's target")
	})
}

// #947: `workspace prune <repo> <branch>` and the prune sweep share one
// removal; it unlinks the lane's links before git deletes the tree.
func TestPruneTicket_NeverDeletesThroughALinkedNodeModules(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		repo, wt, branch, primary := linkedLane(t)
		p, err := PruneTicketPlan(repo, branch, "")
		if err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if err := p.Run(true, &out, &errb); err != nil {
			t.Fatalf("Run: %v\n%s", err, errb.String())
		}
		if _, err := os.Lstat(wt); !os.IsNotExist(err) {
			t.Errorf("the lane must be gone, lstat err = %v", err)
		}
		mustExist(t, primary, "the primary's node_modules")
	})
	t.Run("junction", func(t *testing.T) {
		repo, _, branch, through := junctionLane(t)
		p, err := PruneTicketPlan(repo, branch, "")
		if err != nil {
			t.Fatal(err)
		}
		p.Force = true
		var out, errb bytes.Buffer
		if err := p.Run(true, &out, &errb); err == nil {
			t.Error("a junction that could not be unlinked must stop the prune")
		}
		mustExist(t, through, "the junction's target")
	})
}

// #947: the merged-lane sweep of `workspace prune <repo>`.
func TestPrune_NeverDeletesThroughALinkedNodeModules(t *testing.T) {
	merged := func(t *testing.T, wt, branch string) {
		stubPRState(t, func(_, b string) (string, error) {
			if b == branch {
				return "MERGED", nil
			}
			return "", nil
		})
		head := headSHA(t, wt)
		stubPRHeadOid(t, func(_, b string) (string, error) {
			if b == branch {
				return head, nil
			}
			return "", nil
		})
	}
	t.Run("symlink", func(t *testing.T) {
		repo, wt, branch, primary := linkedLane(t)
		merged(t, wt, branch)
		p, err := PrunePlan(repo)
		if err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if err := p.Run(true, &out, &errb); err != nil {
			t.Fatalf("Run: %v\n%s", err, errb.String())
		}
		if _, err := os.Lstat(wt); !os.IsNotExist(err) {
			t.Errorf("the lane must be gone, lstat err = %v\n%s", err, errb.String())
		}
		mustExist(t, primary, "the primary's node_modules")
	})
	t.Run("junction", func(t *testing.T) {
		repo, wt, branch, through := junctionLane(t)
		merged(t, wt, branch)
		p, err := PrunePlan(repo)
		if err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		_ = p.Run(true, &out, &errb)
		mustExist(t, through, "the junction's target")
	})
}
