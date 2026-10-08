package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// A merge gate that builds in a fresh directory every time starts every
// path-keyed cache cold: Go's build cache and golangci-lint's key on the
// package directory, tsc's tsbuildinfo lives in the checkout. So the gate
// keeps one checkout per repo and purpose under a stable name, resets it to
// the tree being judged before each use, and falls back to a fresh directory
// (correct, only cold) whenever the stable one is held or cannot be trusted.
//
// The names keep the gate-prmerge- prefix every sweep and the canary already
// recognize as a checkout the gate makes.
const (
	prGateWarmName = "gate-prmerge-warm"
	ciWarmName     = "gate-prmerge-localci"
	prGateFresh    = "gate-prmerge-"
)

// prGateKeep is what a reset leaves in the checkout (git clean -e): the holder
// record, and tsc's incremental state, which it validates by content hash and
// which is the one cache that lives in the tree. Build output (dist, .next,
// target) and node_modules are not kept: a stale one is a leak between merges.
var prGateKeep = []string{PRGateHolderFile, "*.tsbuildinfo"}

// prGateCheckout is a checkout to judge a tree in. Release hands it back after
// a normal use; Remove deletes it outright (a signal, a failed merge).
type prGateCheckout struct {
	Path    string
	Warm    bool
	Release func()
	Remove  func()
}

// prGateCheckoutAt makes a checkout of rev for the gate: the warm one named
// name when this process can hold it, else a fresh directory. Only a fresh
// directory that cannot be made is an error.
func prGateCheckoutAt(lane, name, rev string) (prGateCheckout, error) {
	parent := prGateCheckoutParent(lane)
	if parent != "" {
		if co, ok := warmCheckoutAt(lane, filepath.Join(parent, name), rev); ok {
			return co, nil
		}
	}
	return freshCheckoutAt(lane, parent, rev)
}

func freshCheckoutAt(lane, parent, rev string) (prGateCheckout, error) {
	wt, err := os.MkdirTemp(parent, prGateFresh)
	if err != nil {
		return prGateCheckout{}, fmt.Errorf("a checkout could not be created (%v)", err)
	}
	if out, err := git(lane, "worktree", "add", "--detach", wt, rev); err != nil {
		_ = os.RemoveAll(wt)
		return prGateCheckout{}, fmt.Errorf("a checkout of %s could not be made (%v)\n%s", shortCommit(rev), err, strings.TrimSpace(out))
	}
	prGateWriteHolder(wt)
	var once sync.Once
	remove := func() { once.Do(func() { prGateRemoveCheckout(lane, wt) }) }
	return prGateCheckout{Path: wt, Release: remove, Remove: remove}, nil
}

// warmCheckoutAt holds and resets the stable checkout at path. false means the
// caller builds a fresh one: the path is held, or could not be made right.
func warmCheckoutAt(lane, path, rev string) (prGateCheckout, bool) {
	claim := path + ".claim"
	if !takeWarmClaim(claim) {
		return prGateCheckout{}, false
	}
	if !resetWarm(lane, path, rev) {
		prGateRemoveCheckout(lane, path)
		_, _ = git(lane, "worktree", "prune")
		if _, err := git(lane, "worktree", "add", "--detach", path, rev); err != nil {
			_ = os.RemoveAll(path)
			_ = os.Remove(claim)
			return prGateCheckout{}, false
		}
	}
	prGateWriteHolder(path)
	var once sync.Once
	release := func() {
		once.Do(func() {
			_, _ = git(path, "merge", "--abort")
			_ = depinstall.RemoveLinks(path)
			_ = depinstall.RemoveTree(measureTempDir(path))
			_ = os.Remove(claim)
		})
	}
	remove := func() {
		once.Do(func() {
			prGateRemoveCheckout(lane, path)
			_ = os.Remove(claim)
		})
	}
	return prGateCheckout{Path: path, Warm: true, Release: release, Remove: remove}, true
}

// resetWarm puts the checkout at path exactly at rev, with nothing left from
// the last use. It runs inside the gate's own checkout only. false on any
// doubt: not a worktree of this repo, a link that will not unlink, a git step
// that fails.
func resetWarm(lane, path, rev string) bool {
	if fi, err := os.Stat(filepath.Join(path, ".git")); err != nil || fi.IsDir() {
		return false
	}
	common := func(dir string) string {
		out := strings.TrimSpace(gitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir"))
		if r, err := filepath.EvalSymlinks(out); err == nil {
			out = r
		}
		return filepath.Clean(out)
	}
	if mine := common(path); mine == "." || mine != common(lane) {
		return false
	}
	// links first: a clean or a checkout must never reach through a junction
	// into the lane's own node_modules (#947).
	if depinstall.RemoveLinks(path) != nil {
		return false
	}
	_, _ = git(path, "merge", "--abort")
	if _, err := git(path, "checkout", "--force", "--detach", rev); err != nil {
		return false
	}
	args := []string{"clean", "-ffdx"}
	for _, k := range prGateKeep {
		args = append(args, "-e", k)
	}
	_, err := git(path, args...)
	return err == nil
}

// takeWarmClaim takes the exclusive claim on a warm checkout: a file naming
// this pid, created whole and linked into place so a second process sees all
// of it or none. A claim naming a live process is held; one naming a dead
// process is stale and replaced. Never waits.
func takeWarmClaim(claim string) bool {
	tmp := fmt.Sprintf("%s.%d.tmp", claim, os.Getpid())
	if err := os.WriteFile(tmp, []byte("pid="+strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return false
	}
	defer os.Remove(tmp)
	for range 2 {
		if os.Link(tmp, claim) == nil {
			return true
		}
		data, err := os.ReadFile(claim)
		if err != nil {
			continue // vanished between the two: try again
		}
		pid, perr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "pid=")))
		if perr != nil || pidRunningFn(pid) {
			return false
		}
		_ = os.Remove(claim)
	}
	return false
}
