package merge

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
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
	root, trunk := makeForkedRepo(t)
	trunkRev := warmRev(t, root, trunk)
	write(t, root, ".gitignore", "out/\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "ignore out")
	laneRev := warmRev(t, root, "HEAD")

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
	first.Release()

	second, err := prGateCheckoutAt(root, prGateWarmName, trunkRev)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Remove()
	if second.Path != first.Path {
		t.Fatalf("second merge built %s, want the first merge's checkout %s", second.Path, first.Path)
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
