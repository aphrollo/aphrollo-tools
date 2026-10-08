package merge

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
// record only. Build output (dist, .next, target), node_modules and tsc's
// *.tsbuildinfo are not kept. tsc --incremental trusts its tsbuildinfo to say
// what it already emitted, so one kept beside the deleted output would make the
// next tsc skip the emit and fail the build on files the reset removed. That is
// a false red, so the cold tsc run is the price of a reset that is exact.
var prGateKeep = []string{PRGateHolderFile}

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
	// name this process in the holder record before touching the tree: the claim
	// is held, but a sweep reads the record, and it still names the last merge's
	// dead pid until this write.
	prGateWriteHolder(path)
	warmResetting(path)
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
	if _, err := git(path, args...); err != nil {
		return false
	}
	return emptyGitlinks(path)
}

// emptyGitlinks empties every submodule path of the tree at path. The gate
// never initialises or updates submodules, the same as the fresh checkout
// `git worktree add` makes, which leaves a gitlink path an empty directory; but
// git clean skips a populated nested repository, so a submodule something
// populated during one merge would otherwise reach the next. false on any
// doubt.
func emptyGitlinks(path string) bool {
	out, err := git(path, "ls-files", "-z", "--stage")
	if err != nil {
		return false
	}
	for _, rec := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok || !strings.HasPrefix(meta, "160000 ") {
			continue
		}
		dir := filepath.Join(path, filepath.FromSlash(name))
		if !withinCheckout(path, dir) {
			return false
		}
		if os.RemoveAll(dir) != nil || os.MkdirAll(dir, 0o755) != nil {
			return false
		}
	}
	return true
}

// withinCheckout reports whether dir, once the links among its parents are
// resolved, lies inside root. A parent that has become a symlink out of the
// checkout would make a removal reach a directory that is not the gate's. The
// nearest parent that exists is the one resolved; dir itself may be a link, and
// removing a link removes only the link.
func withinCheckout(root, dir string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	parent := filepath.Dir(dir)
	for {
		realParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			rel, rerr := filepath.Rel(realRoot, realParent)
			return rerr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		}
		next := filepath.Dir(parent)
		if next == parent {
			return false
		}
		parent = next
	}
}

// warmClaimJudged runs once a taker has judged a claim stale and before it
// acts; a test uses it to let another taker in at that instant.
// warmResetting runs as the reset of a reused checkout begins, so a test can
// look at what a sweep would see at that instant.
var warmResetting = func(string) {}

// warmIdentityFn names the process holding a pid; a seam so a test can say a
// pid was reused. A var of this package, because the shared one is reached
// through a generated call-through no test can assign.
var warmIdentityFn = func(pid int) (string, bool) { return processIdentityFn(pid) }

// warmLink makes the claim; a seam so a test can fail it the way a file system
// without hard links does.
var warmLink = os.Link

// warmNotef tells the operator why a merge builds cold when that is not the
// ordinary "another merge holds it". One line, to stderr.
var warmNotef = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gate: "+format+"\n", args...)
}

// warmNow is the clock the age bound reads; a seam so a test can move it.
var warmNow = time.Now

var warmClaimSeq atomic.Int64

var warmClaimJudged = func() {}

// takeWarmClaim takes the exclusive claim on a warm checkout: a file naming
// this pid, created whole and linked into place so a second process sees all
// of it or none. A claim naming a live process is held; one naming a dead
// process is stale and replaced. Never waits.
func takeWarmClaim(claim string) bool {
	tmp := fmt.Sprintf("%s.%d.%d.tmp", claim, os.Getpid(), warmClaimSeq.Add(1))
	if err := os.WriteFile(tmp, []byte(warmClaimBody()), 0o644); err != nil {
		return false
	}
	defer os.Remove(tmp)
	for range 2 {
		err := warmLink(tmp, claim)
		if err == nil {
			return true
		}
		if !errors.Is(err, fs.ErrExist) {
			warmNotef("the warm checkout claim %s could not be made (%v): building in a fresh, cold checkout", claim, err)
			return false
		}
		data, err := os.ReadFile(claim)
		if err != nil {
			continue // vanished between the two: try again
		}
		if warmClaimLive(string(data)) {
			return false
		}
		if !replaceStaleClaim(claim, data) {
			return false
		}
	}
	return false
}

// warmClaimMaxAge bounds how long a live pid keeps a claim where the process
// identity cannot be read: no merge gate runs this long.
const warmClaimMaxAge = 12 * time.Hour

// warmClaimBody is the claim this process writes: its pid, when it took the
// claim, and the process identity (boot id and start time) that tells it from
// another process the system gives the same pid later.
func warmClaimBody() string {
	body := fmt.Sprintf("pid=%d\nstarted=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	if id, ok := warmIdentityFn(os.Getpid()); ok {
		body += "id=" + id + "\n"
	}
	return body
}

// warmClaimLive reports whether a claim still names the process that took it.
// A claim that cannot be read is held (unproven means protected); a dead pid and
// a live pid with another identity are stale. Where the identity is missing or
// cannot be read, a claim past warmClaimMaxAge is stale too; a matching identity
// is never aged out.
func warmClaimLive(claim string) bool {
	var pid int
	var id string
	var started time.Time
	havePid := false
	for _, line := range strings.Split(claim, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "pid":
			if n, err := strconv.Atoi(v); err == nil {
				pid, havePid = n, true
			}
		case "id":
			id = v
		case "started":
			started, _ = time.Parse(time.RFC3339Nano, v)
		}
	}
	if !havePid {
		return true
	}
	if !pidRunningFn(pid) {
		return false
	}
	// a readable identity decides: the same process is live however long it has
	// run, another process under the same pid is not
	if cur, ok := warmIdentityFn(pid); ok && id != "" {
		return cur == id
	}
	return started.IsZero() || warmNow().Sub(started) < warmClaimMaxAge
}

// warmTakeoverStale is how old a takeover lock must be before it is taken for
// the leftover of a process that died inside the takeover.
const warmTakeoverStale = time.Minute

// replaceStaleClaim removes the claim holding stale, and only that content. The
// removal runs under a lock only one taker can create, and re-reads the claim
// under it: a taker that judged the claim stale a moment ago cannot remove the
// live claim another taker has put there since. A busy lock is not waited for:
// the caller builds a fresh checkout.
func replaceStaleClaim(claim string, stale []byte) bool {
	lock := claim + ".takeover"
	if err := os.Mkdir(lock, 0o755); err != nil {
		if fi, serr := os.Stat(lock); serr == nil && time.Since(fi.ModTime()) > warmTakeoverStale {
			_ = os.Remove(lock) // a taker that died inside its takeover; the next try is clean
		}
		return false
	}
	defer os.Remove(lock)
	warmClaimJudged()
	now, err := os.ReadFile(claim)
	if err != nil {
		return true // already gone: the link may go ahead
	}
	if !bytes.Equal(now, stale) {
		return false
	}
	return os.Remove(claim) == nil
}
