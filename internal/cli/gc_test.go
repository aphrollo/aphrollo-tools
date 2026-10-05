package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// mkAgedFile writes a file (with parents) and back-dates it, so a test can
// build a target dir that looks idle.
func mkAgedFile(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// TestRunTDDGC_DryRunListsAndDeletesNothing pins --dry: it REPORTS. The
// directory it named must still be there afterwards.
func TestRunTDDGC_DryRunListsAndDeletesNothing(t *testing.T) {
	gateConfigDir(t)
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	code := runGateGC([]string{"--repo", repo, "--dry"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "stale-1a2b") || !strings.Contains(out, "aphrollo gate gc") {
		t.Fatalf("dry run must name the candidate and the command that reclaims it, got:\n%s", out)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("a dry run must delete nothing")
	}
}

// TestRunTDDGC_DefaultDeletesAndReports pins the default: a bare
// `aphrollo gate gc` reclaims the candidates and says what was freed.
func TestRunTDDGC_DefaultDeletesAndReports(t *testing.T) {
	gateConfigDir(t)
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the default run must actually delete the candidate")
	}
	if !strings.Contains(stdout.String(), "freed") {
		t.Fatalf("the default run must report what it freed, got:\n%s", stdout.String())
	}
}

// TestRunTDDGC_LegacyApplyStillDeletesAndNotes pins the one-release bridge:
// --apply is a no-op that executes and says so on stderr.
func TestRunTDDGC_ApplyDeletesAndReports(t *testing.T) {
	gateConfigDir(t)
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("--apply must still delete the candidate")
	}
	if !strings.Contains(stderr.String(), "--apply is a no-op") {
		t.Fatalf("--apply must print its deprecation notice, got stderr:\n%s", stderr.String())
	}
}

// TestRunTDDGC_StrayArgumentIsRefused pins that a positional, which the verb
// does not take, is an error and never a silently ignored word that hides a
// flag written after it.
func TestRunTDDGC_StrayArgumentIsRefused(t *testing.T) {
	gateConfigDir(t)
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "stray", "--dry"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("a refused run must delete nothing")
	}
}

// TestRunTDDGC_FlagAfterTheRepoValueIsHonoured pins flag placement: --dry
// written last still previews.
func TestRunTDDGC_FlagAfterTheRepoValueIsHonoured(t *testing.T) {
	gateConfigDir(t)
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--older-than", "1d", "--dry"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("--dry after other flags must still delete nothing")
	}
}

// TestRunTDDGC_SessionStartSweepStillReclaims pins the detached sweep's own
// argv through the real verb: with no --dry among them it deletes, so the
// default flip cannot turn the background sweep into a silent report.
func TestRunTDDGC_SessionStartSweepStillReclaims(t *testing.T) {
	gateConfigDir(t)
	defer tdd.SetLockDirForTest(t.TempDir())()
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	args := tdd.BackgroundGCArgs(repo)
	if args[0] != "gc" {
		t.Fatalf("background argv = %v, want it to start with the gc verb", args)
	}
	if code := runGateGC(args[1:], &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the session-start sweep left the stale candidate in place")
	}
}

// TestRunTDDGC_OlderThanBoundsWhatQualifies pins --older-than: a cache
// younger than the threshold is not a candidate, and the default is the
// documented 3 days.
func TestRunTDDGC_OlderThanBoundsWhatQualifies(t *testing.T) {
	gateConfigDir(t)
	repo := t.TempDir()
	fiveDays := filepath.Join(repo, "target", "debug", "incremental", "five-days")
	mkAgedFile(t, filepath.Join(fiveDays, "dep-graph.bin"), "01234", 5*24*time.Hour)

	var stdout, stderr bytes.Buffer
	runGateGC([]string{"--repo", repo, "--dry"}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "five-days") {
		t.Fatalf("the default 3d threshold must reclaim a 5-day-old cache, got:\n%s", stdout.String())
	}

	stdout.Reset()
	runGateGC([]string{"--repo", repo, "--older-than", "14d", "--dry"}, &stdout, &stderr)
	if strings.Contains(stdout.String(), "five-days") {
		t.Fatalf("--older-than 14d must spare a 5-day-old cache, got:\n%s", stdout.String())
	}
}

// TestRunTDDGC_RejectsAnUnparseableAge pins the one hard failure: a
// mistyped age must stop the command, never fall back to a default that
// sweeps more than the operator asked for.
func TestRunTDDGC_RejectsAnUnparseableAge(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--older-than", "soon"}, &stdout, &stderr); code == 0 {
		t.Fatal("an unparseable --older-than must fail, not guess")
	}
	if !strings.Contains(stderr.String(), "soon") {
		t.Fatalf("the error must name the bad value, got: %s", stderr.String())
	}
}

// TestRunTDDGC_QuietApplyIsSilentButStillRecordsTheSweep pins the
// background sweep's contract: --quiet prints nothing (it is detached, with
// nowhere to print), and the result is left in the state dir for the next
// session start to surface.
func TestRunTDDGC_QuietApplyIsSilentButStillRecordsTheSweep(t *testing.T) {
	state := gateConfigDir(t)
	repo := t.TempDir()
	stale := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)

	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--quiet"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("--quiet must print nothing, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(state, "gate-state", "gc-last-report.json")); err != nil {
		t.Fatal("a quiet sweep must still record its result for the next session to report")
	}
}

// TestGCFlags_LockAgeReachesTheScan pins the knob's wiring: --lock-age is
// what lets an operator clear TODAY's lock litter on an idle box instead of
// waiting a day for the default bar. A flag parsed but not passed through
// would silently do nothing.
func TestGCFlags_LockAgeReachesTheScan(t *testing.T) {
	scope, err := gcScopeFromFlags("2h")
	if err != nil {
		t.Fatal(err)
	}
	if scope.LockAge != 2*time.Hour {
		t.Fatalf("LockAge = %s, want 2h", scope.LockAge)
	}
	if _, err := gcScopeFromFlags("soon"); err == nil {
		t.Fatal("a junk --lock-age must be rejected, never silently defaulted")
	}
}

// A session opened outside any repo sweeps nothing of its own, so the
// detached sweep asks for --known: every repo the event logs name as worked in
// lately is swept as well, and the OS temp dirs' scratch is walked once.
func TestRunTDDGC_KnownAlsoSweepsTheReposTheGateWorkedIn(t *testing.T) {
	gateConfigDir(t)
	defer tdd.SetLockDirForTest(t.TempDir())()
	other := gitInit(t, map[string]string{"a.txt": "x"})
	stale := filepath.Join(other, "target", "debug", "incremental", "stale-9z")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)
	here := t.TempDir()                                                         // the session's cwd, no repo
	tdd.AppendGateLog("precommit", other, "go vet ./...", "green", time.Second) // the stage line lands in other's event log

	var without, with, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", here, "--dry"}, &without, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if strings.Contains(without.String(), "stale-9z") {
		t.Fatalf("without --known the other repo must not be swept:\n%s", without.String())
	}
	if code := runGateGC([]string{"--repo", here, "--known", "--dry"}, &with, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(with.String(), "stale-9z") {
		t.Fatalf("--known must sweep the repo the event log names:\n%s", with.String())
	}
}

// The scratch of runs that are over is swept once however many repos there
// are: the OS temp dirs are the same for every one of them.
func TestRunTDDGC_KnownWalksTheTempDirScratchOnce(t *testing.T) {
	gateConfigDir(t)
	temp := t.TempDir()
	defer tdd.SetLockDirForTest(temp)()
	mkAgedFile(t, filepath.Join(temp, "go-build4242", "b001", "x.test"), "bin", 40*time.Hour)
	other := gitInit(t, map[string]string{"a.txt": "x"})
	tdd.AppendGateLog("precommit", other, "go vet ./...", "green", time.Second) // the stage line lands in other's event log

	var out, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", t.TempDir(), "--known", "--dry"}, &out, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if got := strings.Count(out.String(), "go-build4242"); got != 1 {
		t.Fatalf("the scratch dir is listed %d times, want once:\n%s", got, out.String())
	}
}

func gcCacheShard(t *testing.T, cache string, age time.Duration) string {
	t.Helper()
	mkAgedFile(t, filepath.Join(cache, "README"), "This directory holds cached build artifacts from the Go build system.\n", 90*24*time.Hour)
	p := filepath.Join(cache, "0a", strings.Repeat("a", 64)+"-d")
	mkAgedFile(t, p, strings.Repeat("x", 100), age)
	return p
}

func gcCapRepo(t *testing.T, cap string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte("[aphrollo]\ngocache-cap = \""+cap+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRunTDDGC_TrimsTheGoCacheOverItsCapAndDryLeavesItBe(t *testing.T) {
	gateConfigDir(t)
	cache := t.TempDir()
	entry := gcCacheShard(t, cache, 40*time.Hour)
	t.Cleanup(tdd.SetGoCacheDirForTest(cache))
	repo := gcCapRepo(t, "1KB") // 100 bytes held is under 1 KB: nothing to trim
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("a cache under its cap lost an entry: %v", err)
	}

	repo = gcCapRepo(t, "0.00000001GB") // about 10 bytes
	stdout.Reset()
	if code := runGateGC([]string{"--repo", repo, "--dry"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "would remove 1 files") {
		t.Errorf("dry output lacks the trim plan:\n%s", stdout.String())
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("--dry removed a cache entry: %v", err)
	}

	stdout.Reset()
	if code := runGateGC([]string{"--repo", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(entry); err == nil {
		t.Error("the executing sweep left an idle entry of an over-cap cache")
	}
	if !strings.Contains(stdout.String(), "removed 1 files") {
		t.Errorf("output lacks the trim result:\n%s", stdout.String())
	}
}

func TestRunTDDGC_BackgroundSweepTrimsTheCacheOnlyOncePerSixHours(t *testing.T) {
	gateConfigDir(t)
	cache := t.TempDir()
	t.Cleanup(tdd.SetGoCacheDirForTest(cache))
	repo := gcCapRepo(t, "0.00000001GB")
	var stdout, stderr bytes.Buffer

	first := gcCacheShard(t, cache, 40*time.Hour)
	if code := runGateGC([]string{"--repo", repo, "--quiet", "--known"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(first); err == nil {
		t.Fatal("the first background sweep did not trim")
	}

	second := gcCacheShard(t, cache, 40*time.Hour)
	if code := runGateGC([]string{"--repo", repo, "--quiet", "--known"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(second); err != nil {
		t.Error("a second background sweep inside six hours trimmed again")
	}
}

func TestRunTDDGC_MalformedGoCacheCapIsRefused(t *testing.T) {
	gateConfigDir(t)
	repo := gcCapRepo(t, "lots")
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--dry"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "gocache-cap") {
		t.Errorf("stderr does not name the key: %s", stderr.String())
	}
}

// Every gate gc run in this package would otherwise trim the operator's real
// Go build cache: no cache unless a test names a fake one.
func init() { tdd.SetGoCacheDirForTest("") }

func TestRunTDDGC_AQuietDryRunDoesNotEvenAskForTheCache(t *testing.T) {
	gateConfigDir(t)
	asked := 0
	t.Cleanup(tdd.SetGoCacheDirFuncForTest(func() string { asked++; return t.TempDir() }))
	repo := gcCapRepo(t, "0.00000001GB")

	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--dry", "--quiet"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}

	if asked != 0 || stdout.Len() != 0 {
		t.Errorf("a dry quiet run asked for the cache %d times and printed %q: it prints nothing, so it must not walk a cache for it", asked, stdout.String())
	}
}
