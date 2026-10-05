package gc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func withScratchHeld(t *testing.T, fn func(string) (bool, bool)) {
	t.Helper()
	prev := scratchHeldFn
	scratchHeldFn = fn
	t.Cleanup(func() { scratchHeldFn = prev })
}

func TestScratchName_OnlyTheShapesTheGatesRunsCreate(t *testing.T) {
	for name, want := range map[string]bool{
		"go-build1234567":                  true,
		"go-build":                         false, // not the go tool's numbered work dir
		"go-buildcache":                    false,
		"TestGatePRMerge_RealSIGTERM12345": true,
		"TestMain":                         false, // no digits: not a t.TempDir
		"aphrollo-tdd-pkgtest-4242":        true,
		"aphrollo-gh-stub99":               true,
		"aphrollo-cli-gh-stub12":           true,
		"aphrollo-lsremote-hang-fixture7":  true,
		"aphrollo-tree-fixture31":          true,
		"aphrollo-lane-abc":                true,
		"aphrollo-ci-run-1234567890":       true,  // a local CI run's scratch, left by a killed run
		"aphrollo-ci-run-":                 false, // no random part: not one a run made
		"replay-1234567890":                true,  // the release replay's clones, left by a replay that was killed
		"replay-":                          false,
		"replay-notes":                     false,
		"aphrollo-mutants-run.lock":        false, // a lock, never scratch
		"aphrollo-build.lock.owner":        false,
		"my-project":                       false,
		"node-compile-cache":               false,
	} {
		if got := scratchName(name); got != want {
			t.Errorf("scratchName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestGCTempScratch_ProposesAFinishedRunsScratchAndNeverAHeldOne(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "go-build111", "b001", "x.test"), "bin", 3*time.Hour)
	mkFile(t, filepath.Join(dir, "go-build222", "b001", "y.test"), "bin", 3*time.Hour)
	mkFile(t, filepath.Join(dir, "go-build333", "b001", "z.test"), "bin", 30*time.Minute)
	mkFile(t, filepath.Join(dir, "notes", "keep.txt"), "mine", 30*24*time.Hour)
	withScratchHeld(t, func(p string) (bool, bool) { return strings.HasSuffix(p, "222"), true })

	got := gcTempScratch(dir, time.Now())

	var paths []string
	for _, c := range got {
		paths = append(paths, filepath.Base(c.Path))
	}
	if len(paths) != 1 || paths[0] != "go-build111" {
		t.Fatalf("proposed %v, want only go-build111: 222 is held by a live process, 333 is 30 minutes old, notes is not scratch", paths)
	}
	if got[0].Kind != GCKindTempLitter || got[0].Size != 3 {
		t.Fatalf("candidate = %+v, want the temp-litter kind and its 3 bytes", got[0])
	}
}

func TestGCTempScratch_TwoHoursIsTheBar(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "go-build1", "f"), "x", scratchMinAge-time.Minute)
	mkFile(t, filepath.Join(dir, "go-build2", "f"), "x", scratchMinAge+time.Minute)
	withScratchHeld(t, func(string) (bool, bool) { return false, true })
	got := gcTempScratch(dir, time.Now())
	if len(got) != 1 || filepath.Base(got[0].Path) != "go-build2" {
		t.Fatalf("proposed %+v, want only the dir older than %s", got, scratchMinAge)
	}
}

func TestGCTempScratch_WhereTheOSCannotSayWhoHoldsItTheBarIsADay(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "go-build1", "f"), "x", 5*time.Hour)
	mkFile(t, filepath.Join(dir, "go-build2", "f"), "x", 25*time.Hour)
	withScratchHeld(t, func(string) (bool, bool) { return false, false })
	got := gcTempScratch(dir, time.Now())
	if len(got) != 1 || filepath.Base(got[0].Path) != "go-build2" {
		t.Fatalf("proposed %+v, want only the day-old dir when liveness is unverifiable", got)
	}
}

func TestGCTempScratch_ASymlinkToScratchIsNeverFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := t.TempDir()
	mkFile(t, filepath.Join(outside, "f"), "x", 5*time.Hour)
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "go-build9")); err != nil {
		t.Fatal(err)
	}
	withScratchHeld(t, func(string) (bool, bool) { return false, true })
	if got := gcTempScratch(dir, time.Now()); len(got) != 0 {
		t.Fatalf("proposed %+v through a symlink, want nothing", got)
	}
}

func TestScanGC_TempScratchScopeSweepsTheLockDirsScratch(t *testing.T) {
	temp := t.TempDir()
	defer SetLockDirForTest(temp)()
	mkFile(t, filepath.Join(temp, "go-build777", "f"), "x", 4*time.Hour)
	withScratchHeld(t, func(string) (bool, bool) { return false, true })

	on := ScanGC(t.TempDir(), 3*24*time.Hour, GCScope{TempScratch: true})
	if _, found := candidateAt(on, filepath.Join(temp, "go-build777")); !found {
		t.Fatalf("TempScratch scope missed the dead go-build dir: %+v", on)
	}
	off := ScanGC(t.TempDir(), 3*24*time.Hour, GCScope{})
	if len(off) != 0 {
		t.Fatalf("a scope without TempScratch proposed %+v", off)
	}
	if !AllGCScopes().TempScratch {
		t.Fatal("the full sweep must include the scratch category")
	}
}

func TestScanGC_ALiveMutationRunHoldsEveryMutationArea(t *testing.T) {
	noMutationRunLive(t)
	repo := makeCargoRepo(t)
	gone := filepath.Join(filepath.Dir(repo), "gate-prmerge-1")
	gitDo(t, repo, "worktree", "add", "-q", "--detach", gone, "HEAD")
	area := measureTempDir(gone)
	mkFile(t, filepath.Join(area, "shard-0", "mutants.out", "outcomes.json"), "[]", 2*time.Hour)
	gitDo(t, repo, "worktree", "remove", "--force", gone)

	prev := mutationRunHeldFn
	t.Cleanup(func() { mutationRunHeldFn = prev })
	mutationRunHeldFn = func() bool { return true }
	if got := ScanGC(repo, 3*24*time.Hour, GCScope{Mutants: true}); len(got) != 0 {
		t.Fatalf("proposed %+v while a mutation run holds the box-wide lock", got)
	}
	mutationRunHeldFn = func() bool { return false }
	if got := ScanGC(repo, 3*24*time.Hour, GCScope{Mutants: true}); len(got) == 0 {
		t.Fatal("the same area must be proposed once no run holds the lock")
	}
}

// The real probe, against a real process: a directory that is some live
// process's working directory is held, and is not once that process is gone.
func TestDirHeldByProcess_ALiveProcessInTheDirHoldsIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs procfs")
	}
	dir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Skipf("no sleep binary: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })

	held, known := dirHeldByProcess(dir)
	if !known || !held {
		t.Fatalf("held=%v known=%v with a live process in %s, want held", held, known, dir)
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the helper process did not exit")
	}
	if held, _ := dirHeldByProcess(dir); held {
		t.Fatal("a directory no process is in must not read as held")
	}
}

// pinTree sets every mtime in the directory tree to at.
func pinTree(t *testing.T, root string, at time.Time) {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		paths = append(paths, p)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// The bar is inclusive: a directory idle for exactly the bar is scratch, one
// a nanosecond younger is not. The clock is passed in, so the edge is exact.
func TestGCTempScratch_ExactlyTheBarIsScratchAndOneNanosecondLessIsNot(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "go-build55", "f"), "x", 0)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pinTree(t, filepath.Join(dir, "go-build55"), at)
	withScratchHeld(t, func(string) (bool, bool) { return false, true })

	if got := gcTempScratch(dir, at.Add(scratchMinAge)); len(got) != 1 {
		t.Fatalf("idle exactly %s: %+v, want proposed", scratchMinAge, got)
	}
	if got := gcTempScratch(dir, at.Add(scratchMinAge-time.Nanosecond)); len(got) != 0 {
		t.Fatalf("idle %s less a nanosecond: %+v, want kept", scratchMinAge, got)
	}
}

// Proposals come back in path order, whatever order the OS lists them.
func TestGCTempScratch_ProposalsAreSortedByPath(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"go-build30", "go-build10", "go-build20"} {
		mkFile(t, filepath.Join(dir, n, "f"), "x", 5*time.Hour)
	}
	withScratchHeld(t, func(string) (bool, bool) { return false, true })
	got := gcTempScratch(dir, time.Now())
	if len(got) != 3 || filepath.Base(got[0].Path) != "go-build10" || filepath.Base(got[1].Path) != "go-build20" || filepath.Base(got[2].Path) != "go-build30" {
		t.Fatalf("proposals = %+v, want go-build10, go-build20, go-build30 in that order", got)
	}
}

// A run that holds a scratch dir only through an open file, with its working
// directory and executable elsewhere, still holds it.
func TestDirHeldByProcess_AnOpenFileInTheDirHoldsIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs procfs")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "held.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `exec 3<"$1"; exec sleep 30`, "sh", file)
	cmd.Dir = t.TempDir()
	if err := cmd.Start(); err != nil {
		t.Skipf("no sh: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })

	// The shell opens the file a moment after it starts: wait, bounded, for
	// the hold to appear.
	deadline := time.Now().Add(10 * time.Second)
	held := false
	for time.Now().Before(deadline) {
		if held, _ = dirHeldByProcess(dir); held {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !held {
		t.Fatal("a process with an open file in the dir did not hold it")
	}
}

// A repo's own go-scratch dir is scanned too: it is where the gate now points
// every go and cargo child's temp dir.
func TestScanGC_TempScratchScopeSweepsTheReposGoScratchDir(t *testing.T) {
	defer SetLockDirForTest(t.TempDir())()
	repo := makeCargoRepo(t)
	root := GoTmpRootDir(repo)
	if root == "" {
		t.Fatal("setup: no go-scratch dir for the repo")
	}
	mkFile(t, filepath.Join(root, "go-build888", "f"), "x", 4*time.Hour)
	withScratchHeld(t, func(string) (bool, bool) { return false, true })

	got := ScanGC(repo, 3*24*time.Hour, GCScope{TempScratch: true})
	if _, found := candidateAt(got, filepath.Join(root, "go-build888")); !found {
		t.Fatalf("the repo's go-scratch dir was not swept: %+v", got)
	}
}
