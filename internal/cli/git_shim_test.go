package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// --- pure-logic tests: no process spawning, no lock, fast -----------------

// TestGitGlobalArgs_TableDriven pins the global-option-vs-verb split
// (requirement 2/3): paired-value options (-C dir, -c k=v, --git-dir path,
// --work-tree path) consume their following token; every other leading
// "-"-prefixed token (e.g. --no-pager) is a standalone global flag.
func TestGitGlobalArgs_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantPrefix []string
		wantRest   []string
	}{
		{"no globals", []string{"commit", "-m", "x"}, nil, []string{"commit", "-m", "x"}},
		{"-C dir", []string{"-C", "/repo", "status"}, []string{"-C", "/repo"}, []string{"status"}},
		{"-c k=v", []string{"-c", "user.name=x", "commit"}, []string{"-c", "user.name=x"}, []string{"commit"}},
		{"--git-dir", []string{"--git-dir", "/repo/.git", "log"}, []string{"--git-dir", "/repo/.git"}, []string{"log"}},
		{"--work-tree", []string{"--work-tree", "/repo", "add", "."}, []string{"--work-tree", "/repo"}, []string{"add", "."}},
		{"--no-pager flag, no value", []string{"--no-pager", "log"}, []string{"--no-pager"}, []string{"log"}},
		{"multiple globals stacked", []string{"-C", "/repo", "--no-pager", "commit"}, []string{"-C", "/repo", "--no-pager"}, []string{"commit"}},
		{"all globals, no verb", []string{"-C", "/repo"}, []string{"-C", "/repo"}, []string{}},
		{"empty", nil, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPrefix, gotRest := gitGlobalArgs(c.args)
			if !reflect.DeepEqual(gotPrefix, c.wantPrefix) {
				t.Fatalf("prefix = %v, want %v", gotPrefix, c.wantPrefix)
			}
			if !reflect.DeepEqual(gotRest, c.wantRest) {
				t.Fatalf("rest = %v, want %v", gotRest, c.wantRest)
			}
		})
	}
}

// TestGitLockScopeFor_TableDriven pins the verb classification table
// (requirement 2), including the three conditional verbs: restore is
// mutating ONLY with --staged, apply ONLY with --index/--cached, worktree
// ONLY for add/remove.
func TestGitLockScopeFor_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		rest []string
		want bool
	}{
		{"add", []string{"add", "."}, true},
		{"commit", []string{"commit", "-m", "x"}, true},
		{"merge", []string{"merge", "other"}, true},
		{"checkout", []string{"checkout", "main"}, true},
		{"switch", []string{"switch", "main"}, true},
		{"reset", []string{"reset", "--hard"}, true},
		{"stash", []string{"stash"}, true},
		{"rm", []string{"rm", "file"}, true},
		{"mv", []string{"mv", "a", "b"}, true},
		{"rebase", []string{"rebase", "main"}, true},
		{"cherry-pick", []string{"cherry-pick", "abc123"}, true},
		{"revert", []string{"revert", "abc123"}, true},
		{"am", []string{"am", "patch"}, true},
		{"pull", []string{"pull"}, true},

		{"status", []string{"status"}, false},
		{"diff", []string{"diff"}, false},
		{"log", []string{"log"}, false},
		{"show", []string{"show", "HEAD"}, false},
		{"rev-parse", []string{"rev-parse", "--git-dir"}, false},
		{"ls-files", []string{"ls-files"}, false},
		{"branch listing", []string{"branch"}, false},
		{"fetch", []string{"fetch"}, true}, // shared state: repo-scoped, but locked
		{"remote", []string{"remote", "-v"}, false},
		{"config get", []string{"config", "user.name"}, false},
		{"blame", []string{"blame", "file"}, false},
		{"grep", []string{"grep", "pattern"}, false},
		{"cat-file", []string{"cat-file", "-p", "HEAD"}, false},
		{"describe", []string{"describe"}, false},
		{"tag listing", []string{"tag"}, false},

		{"restore without --staged", []string{"restore", "file"}, false},
		{"restore --staged", []string{"restore", "--staged", "file"}, true},
		{"apply without index/cached", []string{"apply", "patch"}, false},
		{"apply --index", []string{"apply", "--index", "patch"}, true},
		{"apply --cached", []string{"apply", "--cached", "patch"}, true},
		{"worktree list", []string{"worktree", "list"}, false},
		{"worktree add", []string{"worktree", "add", "../x"}, true},
		{"worktree remove", []string{"worktree", "remove", "../x"}, true},
		{"worktree, no sub-verb", []string{"worktree"}, false},

		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gitLockScopeFor(c.rest) != gitNoLock; got != c.want {
				t.Fatalf("gitLockScopeFor(%v) locked = %v, want %v", c.rest, got, c.want)
			}
		})
	}
}

// TestGitLockScopeFor_HonorsGlobalOptionsBeforeVerb pins that a global
// option ahead of the verb (e.g. -C dir) does not itself get misread as the
// verb -- the caller must skip it via gitGlobalArgs first.
func TestGitLockScopeFor_HonorsGlobalOptionsBeforeVerb(t *testing.T) {
	_, rest := gitGlobalArgs([]string{"-C", "/repo", "commit", "-m", "x"})
	if gitLockScopeFor(rest) != gitWorktreeScope {
		t.Fatal("commit behind a -C global option must still classify as an index mutation")
	}
}

// --- realGit stub -----------------------------------------------------

var (
	gitStubOnce sync.Once
	gitStubPath string
	gitStubErr  error
)

// gitStub returns a tiny compiled Go binary standing in for "realGit":
//   - any invocation containing "--git-common-dir" responds by printing
//     APHROLLO_TEST_GIT_COMMON_DIR to stdout and exiting 0 (or exiting 1,
//     printing nothing, if that env var is unset -- simulating "not a
//     repo", per gitCommonDir's ok=false contract).
//   - every other invocation exits 0, unless APHROLLO_TEST_STUB_FAIL_ON
//     names its own first argument (the verb), in which case it exits 1.
//
// A compiled stub is needed (not a fixed-argv cmd/sh script) because these
// tests need a stand-in that accepts WHATEVER argv the shim actually
// constructs, mirroring cargo_shim_run_test.go's runVerbStub.
func gitStub(t *testing.T) string {
	t.Helper()
	gitStubOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aphrollo-git-stub-")
		if err != nil {
			gitStubErr = err
			return
		}
		src := filepath.Join(dir, "stub.go")
		source := "package main\n\n" +
			"import (\n\t\"os\"\n\t\"fmt\"\n)\n\n" +
			"func main() {\n" +
			"\tfor _, a := range os.Args[1:] {\n" +
			"\t\tif a == \"--git-dir\" {\n" +
			"\t\t\td := os.Getenv(\"APHROLLO_TEST_GIT_DIR\")\n" +
			"\t\t\tif d == \"\" {\n" +
			"\t\t\t\td = os.Getenv(\"APHROLLO_TEST_GIT_COMMON_DIR\")\n" +
			"\t\t\t}\n" +
			"\t\t\tif d == \"\" {\n" +
			"\t\t\t\tos.Exit(1)\n" +
			"\t\t\t}\n" +
			"\t\t\tfmt.Println(d)\n" +
			"\t\t\tos.Exit(0)\n" +
			"\t\t}\n" +
			"\t\tif a == \"--git-common-dir\" {\n" +
			"\t\t\tcd := os.Getenv(\"APHROLLO_TEST_GIT_COMMON_DIR\")\n" +
			"\t\t\tif cd == \"\" {\n" +
			"\t\t\t\tos.Exit(1)\n" +
			"\t\t\t}\n" +
			"\t\t\tfmt.Println(cd)\n" +
			"\t\t\tos.Exit(0)\n" +
			"\t\t}\n" +
			"\t}\n" +
			"\tfailOn := os.Getenv(\"APHROLLO_TEST_STUB_FAIL_ON\")\n" +
			"\tif failOn != \"\" && len(os.Args) > 1 && os.Args[1] == failOn {\n" +
			"\t\tos.Exit(1)\n" +
			"\t}\n" +
			"\tos.Exit(0)\n" +
			"}\n"
		if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
			gitStubErr = err
			return
		}
		out := filepath.Join(dir, "stub")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, src)
		if combined, err := cmd.CombinedOutput(); err != nil {
			gitStubErr = fmt.Errorf("building the git test stub: %v: %s", err, combined)
			return
		}
		gitStubPath = out
	})
	if gitStubErr != nil {
		t.Fatalf("could not build the git test stub: %v", gitStubErr)
	}
	return gitStubPath
}

// withDirectGitShim clears the two "someone above me already holds it"
// passthrough switches for the test's duration. The commit gate runs every
// suite with APHROLLO_GIT_QUEUED=1 (cleanGitEnv marks aphrollo's own git
// children so they never wait on the lock their parent holds), so a test of
// the LOCKING path that inherits it exercises the passthrough instead —
// green under a bare `go test`, red under the very gate it protects.
func withDirectGitShim(t *testing.T) {
	t.Helper()
	t.Setenv(tdd.GitQueuedEnv, "")
	t.Setenv(tdd.BuildLockHeldEnv, "")
}

func testGitShimConfig(t *testing.T) gitShimConfig {
	return gitShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realGit:      gitStub(t),
	}
}

// signalOnFirstWrite wraps a writer and closes ch the first time anything is
// written to it -- used instead of a real-time sleep to synchronize a
// background goroutine (release the lock / remove index.lock) with the
// EXACT moment the shim under test emits its one-shot queued line, so the
// contention tests below need no sleep at all: the release fires the
// instant the shim proves it noticed the contention, not after a guessed
// delay.
type signalOnFirstWrite struct {
	io.Writer
	ch   chan struct{}
	once sync.Once
}

func newSignalOnFirstWrite(w io.Writer) *signalOnFirstWrite {
	return &signalOnFirstWrite{Writer: w, ch: make(chan struct{})}
}

func (s *signalOnFirstWrite) Write(p []byte) (int, error) {
	n, err := s.Writer.Write(p)
	s.once.Do(func() { close(s.ch) })
	return n, err
}

// --- read-only pass-through: never touches the lock ------------------------

// TestRunGitShim_ReadOnlyVerb_NeverTouchesLock pins requirement 2: a
// read-only verb (status) must run straight through -- silent, no lock/
// owner-file interaction at all, proven by the stub recording exactly one
// call. APHROLLO_TEST_GIT_COMMON_DIR is deliberately left unset: if the
// shim mistakenly tried to resolve the repo dir for a read-only verb, the
// commonDir probe would fail (ok=false) and it would STILL pass through per
// gitCommonDir's fallback, so the call-count assertion below is what
// actually pins that a read-only verb never even reaches that code path
// (a second, unwanted rev-parse call would show up as calls[1]).
func TestRunGitShim_ReadOnlyVerb_NeverTouchesLock(t *testing.T) {
	cfg := testGitShimConfig(t)

	var calls [][]string
	execGitHookForTest = func(args []string) {
		calls = append(calls, append([]string{}, args...))
	}
	t.Cleanup(func() { execGitHookForTest = nil })

	var stdout, stderr bytes.Buffer
	code := runGitShim([]string{"status"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0, stderr=%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("read-only pass-through must print nothing, got: %q", stderr.String())
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly ONE real-git call (the read-only verb itself, no rev-parse probe), got %d: %+v", len(calls), calls)
	}
	if !reflect.DeepEqual(calls[0], []string{"status"}) {
		t.Fatalf("call = %+v, want [status] unchanged", calls[0])
	}
}

// --- re-entrancy pass-through -----------------------------------------

// TestRunGitShim_PassthroughWhenGitQueuedEnvSet pins requirement 4: with
// APHROLLO_GIT_QUEUED=1 set, a mutating verb must run straight through
// without ever touching the lock -- proven by using a verb that WOULD
// require commonDir resolution (which would fail here, since
// APHROLLO_TEST_GIT_COMMON_DIR is unset) if the passthrough branch weren't
// taken first.
func TestRunGitShim_PassthroughWhenGitQueuedEnvSet(t *testing.T) {
	t.Setenv(tdd.GitQueuedEnv, "1")
	cfg := testGitShimConfig(t)

	var calls [][]string
	execGitHookForTest = func(args []string) {
		calls = append(calls, append([]string{}, args...))
	}
	t.Cleanup(func() { execGitHookForTest = nil })

	var stdout, stderr bytes.Buffer
	code := runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0, stderr=%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("passthrough must print nothing, got: %q", stderr.String())
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], []string{"commit", "-m", "x"}) {
		t.Fatalf("expected exactly one unchanged call, got %+v", calls)
	}
}

// TestRunGitShim_PassthroughWhenBuildLockHeldEnvSet mirrors the above for
// APHROLLO_BUILD_LOCK_HELD=1 (belt and braces: a nested git call from
// inside an already-locked cargo build must also pass straight through).
func TestRunGitShim_PassthroughWhenBuildLockHeldEnvSet(t *testing.T) {
	t.Setenv(tdd.BuildLockHeldEnv, "1")
	cfg := testGitShimConfig(t)

	var calls [][]string
	execGitHookForTest = func(args []string) {
		calls = append(calls, append([]string{}, args...))
	}
	t.Cleanup(func() { execGitHookForTest = nil })

	var stdout, stderr bytes.Buffer
	code := runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0, stderr=%s", code, stderr.String())
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly one call, got %+v", calls)
	}
}

// --- mutating verb: lock contention -----------------------------------

// gitCommonDirEnv sets up a temp dir as the fake repo's common git dir, so
// gitCommonDir's stub-driven rev-parse resolves to it -- this IS the dir
// the shim's lock/owner files live under, so tests can pre-seed/inspect
// them directly.
func gitCommonDirEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APHROLLO_TEST_GIT_COMMON_DIR", dir)
	return dir
}

// TestRunGitShim_WaitsPrintsQueuedOnceAndAcquiredOnce pins requirement 3:
// a mutating verb contended on the per-repo lock prints exactly ONE queued
// line (naming the holder from the owner file) and exactly ONE acquired
// line, then succeeds once the holder releases. The holder releases the
// instant the queued line is observed (via signalOnFirstWrite), not after
// a guessed delay.
func TestRunGitShim_WaitsPrintsQueuedOnceAndAcquiredOnce(t *testing.T) {
	withDirectGitShim(t)
	commonDir := gitCommonDirEnv(t)
	lockPath := commonDir + "/" + gitLockFileName
	ownerPath := commonDir + "/" + gitOwnerFileName

	release, ok := tdd.TryAcquireFileLock(lockPath)
	if !ok {
		t.Fatal("setup: must be able to take the lock")
	}
	tdd.WriteFileLockOwner(ownerPath, "git commit -m other", "/some/other/repo")

	var realStderr bytes.Buffer
	sig := newSignalOnFirstWrite(&realStderr)
	go func() {
		<-sig.ch
		tdd.RemoveFileLockOwner(ownerPath)
		release()
	}()

	cfg := gitShimConfig{
		waitBudget:   2 * time.Second,
		pollInterval: 5 * time.Millisecond,
		realGit:      gitStub(t),
	}
	var stdout bytes.Buffer
	code := runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, sig, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0, stderr=%s", code, realStderr.String())
	}

	out := realStderr.String()
	if n := strings.Count(out, "queued behind"); n != 1 {
		t.Fatalf("expected exactly ONE queued line, got %d in: %s", n, out)
	}
	if n := strings.Count(out, "lock acquired after"); n != 1 {
		t.Fatalf("expected exactly ONE acquired line, got %d in: %s", n, out)
	}
	if !strings.Contains(out, `"git commit -m other"`) || !strings.Contains(out, "/some/other/repo") {
		t.Fatalf("expected the queued line to name the holder's cmd/cwd, got: %s", out)
	}
}

// TestRunGitShim_GivesUpAfterWaitBudget_Exits75 pins the give-up path: a
// lock held for longer than waitBudget makes the shim give up, exit 75
// (EX_TEMPFAIL), print the give-up line, and never actually run git. The
// lock is held for the test's entire duration (t.Cleanup releases it), so
// no synchronization is needed beyond the shim's own bounded wait.
func TestRunGitShim_GivesUpAfterWaitBudget_Exits75(t *testing.T) {
	withDirectGitShim(t)
	commonDir := gitCommonDirEnv(t)
	lockPath := commonDir + "/" + gitLockFileName

	release, ok := tdd.TryAcquireFileLock(lockPath)
	if !ok {
		t.Fatal("setup: must be able to take the lock")
	}
	t.Cleanup(release)

	cfg := gitShimConfig{
		waitBudget:   80 * time.Millisecond,
		pollInterval: 10 * time.Millisecond,
		realGit:      gitStub(t),
	}
	var calls int
	execGitHookForTest = func(args []string) { calls++ }
	t.Cleanup(func() { execGitHookForTest = nil })

	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, &stderr, cfg)
	elapsed := time.Since(start)

	if code != exGitTempFail {
		t.Fatalf("exit = %d, want %d (EX_TEMPFAIL)", code, exGitTempFail)
	}
	if elapsed > cfg.waitBudget+2*time.Second {
		t.Fatalf("give-up took %s, want close to the %s wait budget", elapsed, cfg.waitBudget)
	}
	if !strings.Contains(stderr.String(), "gave up after") {
		t.Fatalf("expected a give-up line, got: %s", stderr.String())
	}
	if calls != 0 {
		t.Fatalf("expected git to NEVER actually run when giving up, got %d calls", calls)
	}
}

// TestRunGitShim_IndexLockPresentWithoutOwner_WaitsThenProceeds pins
// requirement 3's fallback: a bare index.lock with no owner file (some
// other git process that bypassed the shim entirely) makes the shim print
// the generic "waiting for .git/index.lock" line and wait for it to
// disappear, then proceed once it does -- removed the instant the queued
// line is observed, via signalOnFirstWrite, not after a guessed delay.
func TestRunGitShim_IndexLockPresentWithoutOwner_WaitsThenProceeds(t *testing.T) {
	withDirectGitShim(t)
	commonDir := gitCommonDirEnv(t)
	indexLockPath := commonDir + "/index.lock"
	if err := os.WriteFile(indexLockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	var realStderr bytes.Buffer
	sig := newSignalOnFirstWrite(&realStderr)
	go func() {
		<-sig.ch
		_ = os.Remove(indexLockPath)
	}()

	cfg := gitShimConfig{
		waitBudget:   2 * time.Second,
		pollInterval: 5 * time.Millisecond,
		realGit:      gitStub(t),
	}
	var stdout bytes.Buffer
	code := runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, sig, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0, stderr=%s", code, realStderr.String())
	}
	if !strings.Contains(realStderr.String(), "waiting for .git/index.lock") {
		t.Fatalf("expected the index.lock-without-owner fallback line, got: %s", realStderr.String())
	}
}

// TestRunGitShim_ExitCodePropagation pins that the real git's exit code
// passes straight through for a mutating verb (uncontended lock).
func TestRunGitShim_ExitCodePropagation(t *testing.T) {
	gitCommonDirEnv(t)
	t.Setenv("APHROLLO_TEST_STUB_FAIL_ON", "commit")
	cfg := testGitShimConfig(t)

	var stdout, stderr bytes.Buffer
	code := runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (propagated from the stub)", code)
	}
}

// TestRunGitShim_OwnerFileRemovedAfterRun pins cleanup: once the shim
// finishes, the owner file it wrote while holding the lock must be gone.
func TestRunGitShim_OwnerFileRemovedAfterRun(t *testing.T) {
	commonDir := gitCommonDirEnv(t)
	ownerPath := commonDir + "/" + gitOwnerFileName
	cfg := testGitShimConfig(t)

	var stdout, stderr bytes.Buffer
	runGitShim([]string{"commit", "-m", "x"}, strings.NewReader(""), &stdout, &stderr, cfg)

	if _, ok := tdd.ReadFileLockOwner(ownerPath); ok {
		t.Fatal("owner file must be removed once the shim's run completes")
	}
}

// TestResolveRealGit_HonorsOverride pins the APHROLLO_REAL_GIT override.
func TestResolveRealGit_HonorsOverride(t *testing.T) {
	t.Setenv("APHROLLO_REAL_GIT", "/some/fake/git")
	got, err := resolveRealGit()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/some/fake/git" {
		t.Fatalf("resolveRealGit() = %q, want the override", got)
	}
}
