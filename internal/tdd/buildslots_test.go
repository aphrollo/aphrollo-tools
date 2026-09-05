package tdd

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestResolveTargetDir_HonorsCargoTargetDir pins the first half of the lock
// KEY: when the caller's environment already points cargo at an explicit
// target dir, that — not the workspace's default — is what the build will
// actually write to, so it is what the lock must be keyed on.
//
// Break this catches: keying on the workspace root instead of the target
// dir, which would put two worktrees SHARING one CARGO_TARGET_DIR (borld's
// documented setup) on different keys, letting them build concurrently into
// one directory.
func TestResolveTargetDir_HonorsCargoTargetDir(t *testing.T) {
	ws := t.TempDir()
	shared := filepath.Join(t.TempDir(), "shared-target")
	env := func(k string) string {
		if k == "CARGO_TARGET_DIR" {
			return shared
		}
		return ""
	}
	if got := resolveTargetDir(env, ws); got != filepath.Clean(shared) {
		t.Fatalf("resolveTargetDir = %q, want the explicit CARGO_TARGET_DIR %q", got, shared)
	}
}

// TestResolveTargetDir_FallsBackToWorkspaceTarget pins the other half: with
// no CARGO_TARGET_DIR, cargo writes to <workspace root>/target — resolved
// through cargoWorkspaceRoot, so a MEMBER CRATE keys on the workspace's one
// target dir rather than a nonexistent per-crate one.
func TestResolveTargetDir_FallsBackToWorkspaceTarget(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "Cargo.toml"), []byte("[workspace]\nmembers = [\"crates/a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	crate := filepath.Join(ws, "crates", "a")
	if err := os.MkdirAll(crate, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crate, "Cargo.toml"), []byte("[package]\nname = \"a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "target")
	if got := resolveTargetDir(func(string) string { return "" }, crate); got != want {
		t.Fatalf("resolveTargetDir from a member crate = %q, want the workspace target %q", got, want)
	}
}

// TestTargetLockPath_KeyedOnTargetDir pins that two DIFFERENT target dirs
// key to different locks while one target dir always keys to the same one —
// the whole point of the per-target key.
func TestTargetLockPath_KeyedOnTargetDir(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if targetLockPath(a) == targetLockPath(b) {
		t.Fatalf("distinct target dirs must key to distinct locks, both = %q", targetLockPath(a))
	}
	first := targetLockPath(a)
	if again := targetLockPath(a); first != again {
		t.Fatalf("the same target dir must key to the same lock path on every call, got %q then %q", first, again)
	}
}

// TestBuildSlotLockPath_FoldsPathCaseLikeTheFilesystem pins that one target
// dir spelled with two drive-letter casings — routine on this box, where a
// checkout appears in both — is ONE key on Windows (two keys for one
// directory would let two builds into the same target concurrently, the
// exact failure the key exists to prevent), and equally that a
// case-SENSITIVE filesystem keys two genuinely different paths apart.
func TestBuildSlotLockPath_FoldsPathCaseLikeTheFilesystem(t *testing.T) {
	dir := t.TempDir()
	same := targetLockPath(strings.ToLower(dir)) == targetLockPath(strings.ToUpper(dir))
	if runtime.GOOS == "windows" && !same {
		t.Fatal("on Windows one path spelled in two casings must produce ONE lock key")
	}
	if runtime.GOOS != "windows" && same {
		t.Fatal("on a case-sensitive filesystem two differently-cased paths are two directories, so two keys")
	}
}

// TestTryAcquireBuildSlot_DifferentTargetDirsNeverContend pins the reason
// the key exists at all: a second worktree building into its OWN target dir
// is not competing for the same build directory, so it must not wait on the
// first one's lock while the box still has capacity.
func TestTryAcquireBuildSlot_DifferentTargetDirsNeverContend(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	a, b := t.TempDir(), t.TempDir()

	_, relA, okA := TryAcquireBuildSlot(a, "cargo build", "/repo")
	if !okA {
		t.Fatal("setup: first target dir must acquire")
	}
	defer relA()
	_, relB, okB := TryAcquireBuildSlot(b, "cargo build", "/repo")
	if !okB {
		t.Fatal("a build into a DIFFERENT target dir must not wait on another target dir's lock")
	}
	relB()
}

// TestBuildSlotCount_DefaultsToTwoAndHonorsEnv pins the configured default
// (2) and the override knob.
func TestBuildSlotCount_DefaultsToTwoAndHonorsEnv(t *testing.T) {
	t.Setenv(buildSlotsEnv, "")
	if got := buildSlotCount(); got != 2 {
		t.Fatalf("default slot count = %d, want 2", got)
	}
	t.Setenv(buildSlotsEnv, "5")
	if got := buildSlotCount(); got != 5 {
		t.Fatalf("APHROLLO_BUILD_SLOTS=5 → %d, want 5", got)
	}
	t.Setenv(buildSlotsEnv, "0")
	if got := buildSlotCount(); got != 1 {
		t.Fatalf("a zero/negative slot count must floor at 1, got %d", got)
	}
	t.Setenv(buildSlotsEnv, "nonsense")
	if got := buildSlotCount(); got != 2 {
		t.Fatalf("an unparseable slot count must fall back to the default 2, got %d", got)
	}
}

// TestCargoConfigJobs_ParsesBuildJobs pins the tiny line parser against the
// real shape of ~/.cargo/config.toml, including the two ways it must NOT
// answer: a `jobs` key under a DIFFERENT table, and a commented-out one.
func TestCargoConfigJobs_ParsesBuildJobs(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want int
		ok   bool
	}{
		{"build jobs", "[build]\njobs = 15\nincremental = true\n", 15, true},
		{"tight spacing", "[build]\n  jobs=7\n", 7, true},
		{"inline comment", "[build]\njobs = 15  # shared box\n", 15, true},
		{"other table", "[net]\njobs = 15\n", 0, false},
		{"commented out", "[build]\n# jobs = 15\n", 0, false},
		{"absent", "[build]\nincremental = true\n", 0, false},
		{"non-numeric", "[build]\njobs = \"many\"\n", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(c.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			got, ok := cargoConfigJobs(path)
			if ok != c.ok || got != c.want {
				t.Fatalf("cargoConfigJobs = (%d, %v), want (%d, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}

// TestSlotJobs_SplitsTheTotalAcrossSlots pins the governor's second half:
// N concurrent builds each get 1/N of the box's job budget, so admitting a
// second build does not double the peak link-wave memory the single lock
// used to prevent.
func TestSlotJobs_SplitsTheTotalAcrossSlots(t *testing.T) {
	cases := []struct{ total, slots, want int }{
		{15, 2, 7},
		{24, 4, 6},
		{1, 2, 1}, // never zero — cargo rejects --jobs 0
		{3, 2, 1},
	}
	for _, c := range cases {
		if got := slotJobs(c.total, c.slots); got != c.want {
			t.Errorf("slotJobs(%d, %d) = %d, want %d", c.total, c.slots, got, c.want)
		}
	}
}

// TestEnvWithBuildJobs_KeepsTheStricterCap pins the governor's authority.
// This USED to yield to any caller-set CARGO_BUILD_JOBS, which disengaged
// the cap in exactly the sessions that need it: a lane shell exports the
// variable, and N slots each linking with the whole box's job count is the
// OOM the split exists to prevent. A caller may still ask for LESS.
func TestEnvWithBuildJobs_KeepsTheStricterCap(t *testing.T) {
	got := EnvWithBuildJobs([]string{"PATH=x", "CARGO_BUILD_JOBS=7"}, 3)
	if n := envValue(got, "CARGO_BUILD_JOBS"); n != "3" {
		t.Fatalf("CARGO_BUILD_JOBS = %q, want the slot's stricter 3", n)
	}
	got = EnvWithBuildJobs([]string{"PATH=x", "CARGO_BUILD_JOBS=2"}, 3)
	if n := envValue(got, "CARGO_BUILD_JOBS"); n != "2" {
		t.Fatalf("CARGO_BUILD_JOBS = %q, want the caller's stricter 2", n)
	}
	got = EnvWithBuildJobs([]string{"PATH=x"}, 3)
	if n := envValue(got, "CARGO_BUILD_JOBS"); n != "3" {
		t.Fatalf("CARGO_BUILD_JOBS = %q, want the injected 3", n)
	}
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// TestRunCargoLocked_InjectsBuildJobsForTheChild pins that the jobs cap
// actually reaches the suite subprocess: RunSuite builds the child env from
// os.Environ(), so runCargoLocked must have CARGO_BUILD_JOBS set in THIS
// process while run() executes — and must put the environment back
// afterwards, since one hook process handles many roots.
func TestRunCargoLocked_InjectsBuildJobsForTheChild(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	os.Unsetenv("CARGO_BUILD_JOBS")

	var seen string
	stub := func(Runner, string) SuiteResult {
		seen = os.Getenv("CARGO_BUILD_JOBS")
		return SuiteResult{Passed: true}
	}
	if _, _, acquired := runCargoLocked(stub, Runner{Cmd: "cargo"}, t.TempDir(), time.Second, time.Second); !acquired {
		t.Fatal("setup: the uncontended slot must be acquired")
	}
	n, err := strconv.Atoi(seen)
	if err != nil || n < 1 {
		t.Fatalf("suite saw CARGO_BUILD_JOBS=%q, want a positive integer", seen)
	}
	if want := slotJobs(totalCargoJobs(), 2); n != want {
		t.Fatalf("suite saw CARGO_BUILD_JOBS=%d, want the per-slot share %d", n, want)
	}
	if _, set := os.LookupEnv("CARGO_BUILD_JOBS"); set {
		t.Fatal("CARGO_BUILD_JOBS must be restored (unset) once runCargoLocked returns")
	}
}

// TestRunCargoLocked_CapsACallerSetJobsValue pins the same stricter-wins
// rule at the hook/gate layer. It USED to keep a caller's larger value,
// which is how a lane shell's exported CARGO_BUILD_JOBS disengaged the
// governor for every build that shell started.
func TestRunCargoLocked_CapsACallerSetJobsValue(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv("CARGO_BUILD_JOBS", "1000")

	var seen string
	stub := func(Runner, string) SuiteResult {
		seen = os.Getenv("CARGO_BUILD_JOBS")
		return SuiteResult{Passed: true}
	}
	runCargoLocked(stub, Runner{Cmd: "cargo"}, t.TempDir(), time.Second, time.Second)
	if seen == "1000" {
		t.Fatal("the slot's cap must win over a caller's larger CARGO_BUILD_JOBS")
	}
	if got := os.Getenv("CARGO_BUILD_JOBS"); got != "1000" {
		t.Fatalf("CARGO_BUILD_JOBS = %q after the run, want the caller's own value restored", got)
	}
}

// TestRunCargoLocked_KeysOnTheRunnersOwnTargetDir pins the thread-through:
// two roots whose builds land in DIFFERENT target dirs must not serialise,
// while a run into the HELD target dir still contends.
func TestRunCargoLocked_KeysOnTheRunnersOwnTargetDir(t *testing.T) {
	withIsolatedBuildLock(t)
	os.Unsetenv("CARGO_TARGET_DIR")

	t.Setenv(buildSlotsEnv, "2")
	rootA, rootB := t.TempDir(), t.TempDir()
	_, release, ok := TryAcquireBuildSlot(resolveTargetDir(os.Getenv, rootA), "cargo build", "/repo")
	if !ok {
		t.Fatal("setup: root A's only slot must be takeable")
	}
	defer release()

	stub := func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }
	if _, _, acquired := runCargoLocked(stub, Runner{Cmd: "cargo"}, rootB, 50*time.Millisecond, time.Second); !acquired {
		t.Fatal("a run whose target dir is a DIFFERENT directory must not wait on root A's slot")
	}
	if _, _, acquired := runCargoLocked(stub, Runner{Cmd: "cargo"}, rootA, 50*time.Millisecond, time.Second); acquired {
		t.Fatal("a run into root A's OWN target dir must still contend with the holder")
	}
}
