package merge

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// warmRev is the commit sha a ref names in repo.
func warmRev(t *testing.T, repo, ref string) string {
	t.Helper()
	return strings.TrimSpace(gitOutT(t, repo, "rev-parse", ref+"^{commit}"))
}

// warmTreeIs fails unless wt holds exactly rev's files: every tracked path of
// rev present, nothing untracked or ignored beside the holder record.
func warmTreeIs(t *testing.T, wt, rev string) {
	t.Helper()
	if head := warmRev(t, wt, "HEAD"); head != rev {
		t.Fatalf("checkout %s is at %s, want %s", wt, head, rev)
	}
	var left []string
	for _, l := range strings.Split(gitOutT(t, wt, "status", "--porcelain", "--ignored", "--untracked-files=all"), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasSuffix(l, PRGateHolderFile) {
			left = append(left, l)
		}
	}
	if len(left) != 0 {
		t.Fatalf("checkout %s differs from %s: %v", wt, rev, left)
	}
}

func warmDeadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("git", "--version")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running a throwaway child: %v", err)
	}
	return cmd.Process.Pid
}

// Two merges in a row land in one checkout path, and the second sees nothing
// the first left: not a file only its tree had, not an untracked file, not an
// ignored build output.
func TestWarmGate_SecondUseReusesThePathAndLeaksNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := makeForkedRepo(t)
	write(t, root, ".gitignore", "out/\n")
	write(t, root, "lane-only.txt", "tracked on the lane, absent on the next tree\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "ignore out")
	laneRev := warmRev(t, root, "HEAD")
	// the next merge's tree keeps the .gitignore (so out/ stays an ignored
	// directory the reset must clear with -x) but lacks lane-only.txt
	gitDo(t, root, "rm", "-q", "lane-only.txt")
	write(t, root, "next.txt", "the next merge's own file\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "next merge tree")
	trunkRev := warmRev(t, root, "HEAD")

	first, err := prGateCheckoutAt(root, prGateWarmName, laneRev)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Warm {
		t.Fatalf("the first use must build the warm checkout, got a fresh one at %s", first.Path)
	}
	warmTreeIs(t, first.Path, laneRev)
	write(t, first.Path, "untracked.txt", "left by merge one\n")
	write(t, first.Path, "out/build.bin", "ignored output of merge one\n")
	write(t, first.Path, "newdir/inner.txt", "an untracked directory of merge one\n")
	first.Release()

	second, err := prGateCheckoutAt(root, prGateWarmName, trunkRev)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Remove()
	if second.Path != first.Path {
		t.Fatalf("second merge built %s, want the first merge's checkout %s", second.Path, first.Path)
	}
	for _, p := range []string{"untracked.txt", "out/build.bin", "newdir/inner.txt", "lane-only.txt"} {
		if _, err := os.Stat(filepath.Join(second.Path, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived into the second merge (stat err: %v)", p, err)
		}
	}
	warmTreeIs(t, second.Path, trunkRev)
	if got := strings.TrimSpace(gitOutT(t, root, "diff", "--name-only", laneRev, trunkRev)); got == "" {
		t.Fatal("the two revisions are identical, so the leak check proves nothing")
	}
}

// A checkout a live process holds is never waited for or shared: the second
// caller gets a fresh directory of its own, which its release removes.
func TestWarmGate_HeldCheckoutFallsBackToAFreshOneAndRemovesIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	rev := warmRev(t, root, trunk)

	held, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Remove()
	other, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	if other.Warm || other.Path == held.Path {
		t.Fatalf("a held checkout was shared: held %s, other %s (warm=%v)", held.Path, other.Path, other.Warm)
	}
	if !strings.HasPrefix(filepath.Base(other.Path), "gate-prmerge-") {
		t.Fatalf("the fallback %s is not in the gate-prmerge-* shape sweeps know", other.Path)
	}
	warmTreeIs(t, other.Path, rev)
	other.Release()
	if _, err := os.Stat(other.Path); !os.IsNotExist(err) {
		t.Fatalf("the fresh fallback outlived its release (stat err: %v)", err)
	}
	if _, err := os.Stat(held.Path); err != nil {
		t.Fatalf("releasing the fallback removed the held checkout: %v", err)
	}
}

// A claim left by a process that died does not hold the checkout.
func TestWarmGate_ClaimOfADeadProcessIsReclaimed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	rev := warmRev(t, root, trunk)
	first, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	// the process died holding it: its claim stays, naming a dead pid
	claim := filepath.Join(filepath.Dir(first.Path), prGateWarmName+".claim")
	if err := os.WriteFile(claim, []byte("pid="+strconv.Itoa(warmDeadPid(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Remove()
	if !again.Warm || again.Path != first.Path {
		t.Fatalf("a dead holder's claim kept the checkout: got %s warm=%v, want %s", again.Path, again.Warm, first.Path)
	}
}

// A stable path that is not a worktree of this repo is removed and made again,
// never a failed merge.
func TestWarmGate_BrokenCheckoutIsRebuilt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	rev := warmRev(t, root, trunk)
	parent := prGateCheckoutParent(root)
	if err := os.MkdirAll(filepath.Join(parent, prGateWarmName), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(parent, prGateWarmName), "junk.txt", "not a worktree\n")

	co, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatalf("a broken warm checkout failed the merge: %v", err)
	}
	defer co.Remove()
	if !co.Warm || filepath.Base(co.Path) != prGateWarmName {
		t.Fatalf("got %s warm=%v, want the warm path rebuilt", co.Path, co.Warm)
	}
	warmTreeIs(t, co.Path, rev)
}

// The merge gate and local CI keep separate checkouts: one purpose never
// resets the other's.
func TestWarmGate_PurposesKeepSeparateCheckouts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	rev := warmRev(t, root, trunk)
	a, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	b, err := prGateCheckoutAt(root, ciWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Remove()
	if !a.Warm || !b.Warm || a.Path == b.Path {
		t.Fatalf("premerge %s (warm=%v) and local CI %s (warm=%v) must be two warm checkouts", a.Path, a.Warm, b.Path, b.Warm)
	}
}

// Through the real gate: two merges judge in one directory.
func TestGatePRMerge_TwoMergesJudgeInOneWarmCheckout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)
	declareMutantsAtMergeCommitted(t, root)
	var seen []gateRun
	for i := 0; i < 2; i++ {
		if err := GatePRMerge(root, "", recordRuns(&seen, SuiteResult{Passed: true}), io.Discard); err != nil {
			t.Fatalf("merge %d: %v", i+1, err)
		}
	}
	dirs := map[string]bool{}
	for _, r := range seen {
		// a suite runs in a crate or package dir under the checkout: name the
		// checkout itself
		d := r.Dir
		for filepath.Base(d) != prGateWarmName && filepath.Dir(d) != d {
			d = filepath.Dir(d)
		}
		dirs[d] = true
	}
	var got []string
	for d := range dirs {
		got = append(got, d)
	}
	sort.Strings(got)
	if len(got) != 1 || filepath.Base(got[0]) != prGateWarmName {
		t.Fatalf("suites ran in %v, want one directory named %s", got, prGateWarmName)
	}
}

// The reason for reuse: a Go build in the reused checkout is a cache hit. The
// cache is keyed by the package directory, so the same path twice adds no
// entry, which a fresh directory per merge would.
func TestWarmGate_GoBuildInTheReusedCheckoutAddsNoCacheEntries(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cache := t.TempDir()
	t.Setenv("GOCACHE", cache)
	t.Setenv("GOFLAGS", "")
	root, trunk := makeForkedRepo(t)
	write(t, root, "go.mod", "module warmprobe\n\ngo 1.22\n")
	write(t, root, "main.go", "package main\n\nfunc main() { println(\"hi\") }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "a go module")
	rev := warmRev(t, root, trunk)
	if out := strings.TrimSpace(gitOutT(t, root, "ls-tree", "--name-only", "HEAD")); !strings.Contains(out, "main.go") {
		rev = warmRev(t, root, "HEAD")
	}

	count := func() int {
		n := 0
		_ = filepath.WalkDir(cache, func(_ string, d os.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				n++
			}
			return nil
		})
		return n
	}
	build := func(wt string) {
		cmd := exec.Command("go", "build", "-o", os.DevNull, "./...")
		cmd.Dir = wt
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build in %s: %v\n%s", wt, err, out)
		}
	}
	first, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	build(first.Path)
	first.Release()
	after1 := count()

	second, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Remove()
	build(second.Path)
	if after2 := count(); after2 != after1 {
		t.Fatalf("the second build added %d cache entries (%d -> %d), want 0: the reused path is not a hit", after2-after1, after1, after2)
	}
	t.Logf("GOCACHE files after the first build: %d, after the second: %d", after1, count())
}

// Two takers meet one stale claim: exactly one wins. The second arrives while
// the first has judged the claim stale and not yet replaced it; a takeover that
// removes whatever it finds would take the first one's live claim away.
func TestWarmClaim_TwoTakersOnOneStaleClaimExactlyOneWins(t *testing.T) {
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if err := os.WriteFile(claim, []byte("pid="+strconv.Itoa(warmDeadPid(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wins := 0
	inner := false
	warmClaimJudged = func() {
		if inner {
			return
		}
		inner = true
		if takeWarmClaim(claim) {
			wins++
		}
	}
	t.Cleanup(func() { warmClaimJudged = func() {} })
	if takeWarmClaim(claim) {
		wins++
	}
	if wins != 1 {
		t.Fatalf("%d takers won one stale claim, want exactly 1", wins)
	}
}

// While a reused checkout is being reset it already names the process doing it:
// a sweep that reads the holder record at that instant must see a live pid, not
// the dead one of the last merge.
func TestWarmGate_HolderNamesTheTakerBeforeTheReset(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	rev := warmRev(t, root, trunk)
	first, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	holder := filepath.Join(first.Path, PRGateHolderFile)
	first.Release()
	if err := os.WriteFile(holder, []byte("pid="+strconv.Itoa(warmDeadPid(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var seen string
	warmResetting = func(string) {
		b, _ := os.ReadFile(holder)
		seen = string(b)
	}
	t.Cleanup(func() { warmResetting = func(string) {} })
	again, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Remove()
	if want := "pid=" + strconv.Itoa(os.Getpid()) + "\n"; !strings.HasPrefix(seen, want) {
		t.Fatalf("at the start of the reset the holder record reads %q, want it to begin %q", seen, want)
	}
}

func warmWriteClaim(t *testing.T, body string) string {
	t.Helper()
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if err := os.WriteFile(claim, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return claim
}

// A pid the system handed out again after the claimant died names a different
// process: the claim carries the identity it was stamped with, and a live pid
// with another identity is stale. The same identity is still held.
func TestWarmClaim_ReusedPidWithAnotherIdentityIsStale(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	body := "pid=" + pid + "\nstarted=" + time.Now().UTC().Format(time.RFC3339Nano) + "\nid=boot:111\n"
	old := warmIdentityFn
	t.Cleanup(func() { warmIdentityFn = old })

	warmIdentityFn = func(int) (string, bool) { return "boot:111", true }
	if takeWarmClaim(warmWriteClaim(t, body)) {
		t.Fatal("a claim whose pid and identity both match a live process was taken")
	}
	warmIdentityFn = func(int) (string, bool) { return "boot:222", true }
	if !takeWarmClaim(warmWriteClaim(t, body)) {
		t.Fatal("a claim whose pid now belongs to another process held the checkout")
	}
}

// Where no identity can be read, a live pid keeps a claim only for a bounded
// time: a claim older than the bound is stale, a recent one is held.
func TestWarmClaim_LivePidWithAnOldClaimIsStale(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	at := func(ago time.Duration) string {
		return "pid=" + pid + "\nstarted=" + time.Now().Add(-ago).UTC().Format(time.RFC3339Nano) + "\n"
	}
	if takeWarmClaim(warmWriteClaim(t, at(time.Minute))) {
		t.Fatal("a recent claim of a live pid was taken")
	}
	if !takeWarmClaim(warmWriteClaim(t, at(warmClaimMaxAge+time.Hour))) {
		t.Fatal("a claim older than the bound still held the checkout")
	}
}

// The claim a taker writes carries what the stale checks read: its pid, when it
// took the claim, and its process identity.
func TestWarmClaim_TheClaimWrittenCarriesPidStartAndIdentity(t *testing.T) {
	old := warmIdentityFn
	t.Cleanup(func() { warmIdentityFn = old })
	warmIdentityFn = func(int) (string, bool) { return "boot:777", true }
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if !takeWarmClaim(claim) {
		t.Fatal("a free claim was not taken")
	}
	b, err := os.ReadFile(claim)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "pid="+strconv.Itoa(os.Getpid()) || !strings.HasPrefix(lines[1], "started=") || lines[2] != "id=boot:777" {
		t.Fatalf("claim reads %q, want pid, started and id lines", lines)
	}
}

// tsc --incremental trusts its tsbuildinfo to say what was already emitted. The
// reset deletes the build output, so a tsbuildinfo left behind would make the
// next tsc skip the emit and the build run against missing files: a false red.
// It goes with the output, whether it sits in the output dir or beside the
// tsconfig.
func TestWarmGate_ATsbuildinfoDoesNotOutliveTheOutputItDescribes(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := makeForkedRepo(t)
	write(t, root, ".gitignore", "dist/\n*.tsbuildinfo\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "ignore tsc output")
	rev := warmRev(t, root, "HEAD")

	first, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	write(t, first.Path, "tsconfig.tsbuildinfo", "{}\n")
	write(t, first.Path, "pkg/tsconfig.tsbuildinfo", "{}\n")
	write(t, first.Path, "dist/tsconfig.tsbuildinfo", "{}\n")
	write(t, first.Path, "dist/index.js", "x\n")
	first.Release()

	second, err := prGateCheckoutAt(root, prGateWarmName, rev)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Remove()
	for _, p := range []string{"tsconfig.tsbuildinfo", "pkg/tsconfig.tsbuildinfo", "dist/tsconfig.tsbuildinfo", "dist/index.js"} {
		if _, err := os.Stat(filepath.Join(second.Path, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived the reset (stat err: %v), so tsc would skip an emit the reset deleted", p, err)
		}
	}
}

// A file system that cannot hard-link (FAT, some network mounts) makes every
// merge cold; the gate says so once, with the reason, instead of going quiet.
func TestWarmClaim_ALinkThatCannotBeMadeIsSaidOnce(t *testing.T) {
	var notes []string
	oldLink, oldNote := warmLink, warmNotef
	t.Cleanup(func() { warmLink, warmNotef = oldLink, oldNote })
	warmLink = func(string, string) error { return errors.New("operation not supported") }
	warmNotef = func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) }
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if takeWarmClaim(claim) {
		t.Fatal("a claim was taken although the link failed")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "operation not supported") || !strings.Contains(notes[0], claim) {
		t.Fatalf("notes = %q, want one line naming the claim and the link error", notes)
	}
}

// The reset never initialises or updates submodules, the same as the fresh
// checkout it replaces (`git worktree add` leaves a gitlink path empty): a
// submodule populated by one merge is emptied for the next, and the checkout
// stays warm. Pinned so a later change that starts updating them does so on
// purpose, and the gate's tree stays exactly the merge tree.
func TestWarmGate_SubmodulePathsStayEmptyLikeAFreshCheckout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	rev := warmRev(t, root, trunk)
	gitDo(t, root, "update-index", "--add", "--cacheinfo", "160000,"+rev+",vendor/sub")
	gitDo(t, root, "commit", "-qm", "a gitlink")
	withLink := warmRev(t, root, "HEAD")

	first, err := prGateCheckoutAt(root, prGateWarmName, withLink)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(first.Path, "vendor", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDo(t, sub, "init", "-q")
	write(t, sub, "populated.txt", "left by a submodule update\n")
	first.Release()

	second, err := prGateCheckoutAt(root, prGateWarmName, withLink)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Remove()
	if !second.Warm || second.Path != first.Path {
		t.Fatalf("a populated submodule path made the reset give up: got %s warm=%v", second.Path, second.Warm)
	}
	entries, _ := os.ReadDir(filepath.Join(second.Path, "vendor", "sub"))
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the submodule path holds %v after the reset, want it empty as in a fresh checkout", names)
	}
}

// A path under a gitlink that has become a symlink out of the checkout is never
// followed: the directory it points to survives and the reset gives up, so the
// caller rebuilds the checkout cold.
func TestEmptyGitlinks_NeverFollowsASymlinkOutOfTheCheckout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	rev := warmRev(t, root, trunk)
	gitDo(t, root, "update-index", "--add", "--cacheinfo", "160000,"+rev+",a/b")
	gitDo(t, root, "commit", "-qm", "a gitlink under a")
	wt := filepath.Join(t.TempDir(), "wt")
	gitDo(t, root, "worktree", "add", "--detach", wt, warmRev(t, root, "HEAD"))
	outside := t.TempDir()
	write(t, outside, "b/precious.txt", "outside the checkout\n")
	if err := os.RemoveAll(filepath.Join(wt, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(wt, "a")); err != nil {
		t.Skipf("symlinks unavailable: %v", err) // skip-ok: creating a symlink needs a privilege some hosts withhold
	}
	if emptyGitlinks(wt) {
		t.Error("a gitlink path behind a symlink out of the checkout was emptied")
	}
	if _, err := os.Stat(filepath.Join(outside, "b", "precious.txt")); err != nil {
		t.Fatalf("the directory outside the checkout was deleted through the symlink: %v", err)
	}
}
