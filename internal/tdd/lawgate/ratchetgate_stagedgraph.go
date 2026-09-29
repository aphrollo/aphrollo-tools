package lawgate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// commitRatchetStage is the commit gate's ratchet stage. Every other law
// already reads the staged tree through the index overlay; the dep-graph
// laws ask cargo or go, which read the disk they run in, so at commit they
// run in the gate worktree with the staged tree read into it — the one
// fail-first's GREEN run uses — whenever the disk is not the index.
func commitRatchetStage(repoRoot string) GateResult {
	tree := stagedGraphTree{repoRoot: repoRoot}
	defer tree.remove()
	return judgeRatchet("precommit", repoRoot, tree.get)
}

// stagedGraphTree is the checkout of the index one commit's dep-graph laws
// query, made on first use.
type stagedGraphTree struct {
	repoRoot string
	wt       string
}

// get returns the tree to query: repoRoot itself when its disk holds
// exactly the index, else a fresh checkout of the index. In the checkout
// cargo gets a target dir of its own, and --offline when the staged
// Cargo.lock is HEAD's, which cargo has already resolved.
func (s *stagedGraphTree) get() (ratchet.GraphTree, error) {
	if diskIsIndex(s.repoRoot) {
		return ratchet.GraphTree{Dir: s.repoRoot}, nil
	}
	wt, err := addGateWorktree(s.repoRoot)
	if err != nil {
		return ratchet.GraphTree{}, err
	}
	s.wt = wt
	if out, err := readStagedTree(s.repoRoot, wt); err != nil {
		return ratchet.GraphTree{}, fmt.Errorf("reading the staged tree into %s: %s: %w", wt, out, err)
	}
	return ratchet.GraphTree{
		Dir:            wt,
		CargoOffline:   stagedLockIsHead(s.repoRoot),
		CargoTargetDir: filepath.Join(wt, "target"),
	}, nil
}

// remove deletes the checkout, when get made one.
func (s *stagedGraphTree) remove() {
	if s.wt != "" {
		removeGateWorktree(s.repoRoot, s.wt)
	}
}

// diskIsIndex reports whether repoRoot's working tree holds exactly its
// index: no tracked file differs from its staged copy and no untracked file
// sits beside them. A git failure answers false, which only costs a checkout.
func diskIsIndex(repoRoot string) bool {
	changed, err := git(repoRoot, "diff", "--name-only")
	if err != nil || strings.TrimSpace(changed) != "" {
		return false
	}
	untracked, err := git(repoRoot, "ls-files", "--others", "--exclude-standard")
	return err == nil && strings.TrimSpace(untracked) == ""
}

// stagedLockIsHead reports whether the staged Cargo.lock is the one HEAD
// carries.
func stagedLockIsHead(repoRoot string) bool {
	staged, err := git(repoRoot, "rev-parse", "-q", "--verify", ":Cargo.lock")
	if err != nil {
		return false
	}
	head, _ := git(repoRoot, "rev-parse", "-q", "--verify", "HEAD:Cargo.lock")
	return strings.TrimSpace(staged) == strings.TrimSpace(head)
}
