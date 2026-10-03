package gitiso

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The probe is the child half of VerifyNoLeak.
func TestGitIsolation_Probe(t *testing.T) { Probe(t) }

// A hook exports GIT_DIR and friends, and a run can start inside a repository
// or with its temp dir in one; Main is what keeps the probe's bare git calls
// off all of it.
func TestMain_KeepsBareGitCallsOffTheRepositoriesAndConfigAroundTheRun(t *testing.T) {
	VerifyNoLeak(t, "TestGitIsolation_Probe")
}

func TestEnclosingRepo_FindsTheNearestDirHoldingDotGit(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	deep := filepath.Join(inner, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// .git as a file, the way a linked worktree has it.
	if err := os.WriteFile(filepath.Join(inner, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outer, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := enclosingRepo(deep); got != inner {
		t.Errorf("enclosingRepo(%s) = %q, want %q", deep, got, inner)
	}
	if got := enclosingRepo(inner); got != inner {
		t.Errorf("a dir holding .git is its own repo: got %q, want %q", got, inner)
	}
	if got := enclosingRepo(outer); got != outer {
		t.Errorf("enclosingRepo(%s) = %q, want %q", outer, got, outer)
	}
}

func TestEnclosingRepo_AnswersEmptyForNoDir(t *testing.T) {
	if got := enclosingRepo(""); got != "" {
		t.Errorf("enclosingRepo of no dir = %q, want empty", got)
	}
}

// The walk ends at the filesystem root instead of turning on it.
func TestEnclosingRepo_StopsAtTheFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
		t.Skip("the filesystem root holds a .git") // skip-ok: an environment probe.
	}

	if got := enclosingRepo(root); got != "" {
		t.Errorf("enclosingRepo(%q) = %q, want empty", root, got)
	}
}

// homeVars is every variable homeLayout must place.
var homeVars = []string{
	"HOME", "USERPROFILE",
	"APPDATA", "LOCALAPPDATA",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
}

// inside reports whether path is dir or under it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Main hands the package's tests a run whose git world is a temp root: the
// config, the temp dir and every home variable inside it, the system config
// off, and the ceiling naming the root.
func TestIsolate_PutsTheGitConfigTempDirAndHomeUnderTheRunsRoot(t *testing.T) {
	root := filepath.Dir(os.Getenv("HOME"))
	for _, name := range append([]string{"GIT_CONFIG_GLOBAL", "TMPDIR", "TMP", "TEMP", "GOTMPDIR"}, homeVars...) {
		if v := os.Getenv(name); v == "" || !inside(root, v) {
			t.Errorf("%s = %q, want a path under the run's root %q", name, v, root)
		}
	}
	if got := os.Getenv("GIT_CONFIG_NOSYSTEM"); got != "1" {
		t.Errorf("GIT_CONFIG_NOSYSTEM = %q, want 1", got)
	}
	if got := os.Getenv("GIT_CEILING_DIRECTORIES"); !strings.Contains(got, root) {
		t.Errorf("GIT_CEILING_DIRECTORIES = %q, want it to name the run's root %q", got, root)
	}
	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "4" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want 4: auto maintenance off and the repo hooks off", got)
	}
}

// Nested temp roots (a probe that runs a whole test binary inside a test's own
// temp dir, under a gate checkout) pass Windows' 260-character limit, and git
// refuses a longer path unless it is told to take it: "Filename too long".
func TestIsolate_TellsGitToTakeLongPaths(t *testing.T) {
	out, err := exec.Command("git", "config", "--global", "--get", "core.longpaths").Output() // stderr-ok: the value is asserted below
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Fatalf("git config --global core.longpaths = %q (%v), want true", out, err)
	}
}

// The run started in a checkout, so the checkout's root is a ceiling too:
// git run from the package directory cannot walk up into it.
func TestIsolate_NamesTheCheckoutTheRunStartedInAsACeiling(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := enclosingRepo(cwd)
	if repo == "" {
		t.Skip("not started inside a checkout") // skip-ok: an environment probe.
	}
	if got := os.Getenv("GIT_CEILING_DIRECTORIES"); !strings.Contains(got, repo) {
		t.Errorf("GIT_CEILING_DIRECTORIES = %q, want it to name the checkout %q", got, repo)
	}
}

// Moving the home must not move the toolchain: with the caches pinned to where
// they resolved before the move, no `go` a test spawns rebuilds the world.
func TestPinToolchainHomes_KeepsTheGoCachesOutOfTheTempHome(t *testing.T) {
	root := filepath.Dir(os.Getenv("HOME"))
	for _, name := range []string{"GOPATH", "GOCACHE", "GOMODCACHE", "GOENV"} {
		v := os.Getenv(name)
		if v == "" || inside(root, v) {
			t.Errorf("%s = %q, want the toolchain's own location, outside the temp root %q", name, v, root)
		}
	}
}

func TestPinToolchainHomes_LeavesTheEnvironmentAloneWhenGoCannotAnswer(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GOCACHE", "/keep/this")
	pinToolchainHomes()
	if got := os.Getenv("GOCACHE"); got != "/keep/this" {
		t.Fatalf("GOCACHE = %q after a failed lookup, want it untouched", got)
	}
}

func TestPinToolchainHomes_PointsRustupAtTheHomeItReplaces_ButKeepsOneThatIsSet(t *testing.T) {
	t.Setenv("RUSTUP_HOME", "")
	pinToolchainHomes()
	home, _ := os.UserHomeDir()
	if got, want := os.Getenv("RUSTUP_HOME"), filepath.Join(home, ".rustup"); got != want {
		t.Fatalf("RUSTUP_HOME = %q, want %q", got, want)
	}
	t.Setenv("RUSTUP_HOME", "/opt/rustup")
	pinToolchainHomes()
	if got := os.Getenv("RUSTUP_HOME"); got != "/opt/rustup" {
		t.Fatalf("RUSTUP_HOME = %q, want the one that was already set", got)
	}
}

func TestHomeLayout_PutsEveryVariableUnderTheFakeHome(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "home")
	layout := homeLayout(fake)
	if len(layout) != len(homeVars) {
		t.Fatalf("layout has %d entries for %d variables", len(layout), len(homeVars))
	}
	for _, name := range homeVars {
		path, ok := layout[name]
		if !ok || !inside(fake, path) {
			t.Errorf("%s -> %q (present=%v), want a path under %q", name, path, ok, fake)
		}
	}
	if layout["USERPROFILE"] != fake || layout["HOME"] != fake {
		t.Errorf("HOME/USERPROFILE = %q/%q, want the fake home itself %q", layout["HOME"], layout["USERPROFILE"], fake)
	}
}

// The run's git world is made of directories and one file under the root, and a
// root that cannot hold them is an error, never a half-isolated run.
func TestIsolate_FailsWhenTheRootCannotHoldItsTempDirHomeOrGitConfig(t *testing.T) {
	blocked := func(name string, setup func(root string) error) {
		t.Helper()
		root := t.TempDir()
		if err := setup(root); err != nil {
			t.Fatal(err)
		}
		if _, err := Isolate(root); err == nil {
			t.Errorf("%s: Isolate answered no error", name)
		}
	}
	blocked("tmp is a file", func(root string) error {
		return os.WriteFile(filepath.Join(root, "tmp"), nil, 0o644)
	})
	blocked("home is a file", func(root string) error {
		return os.WriteFile(filepath.Join(root, "home"), nil, 0o644)
	})
	blocked(".gitconfig is a directory", func(root string) error {
		return os.MkdirAll(filepath.Join(root, "home", ".gitconfig"), 0o755)
	})
}

func TestMustIsolate_PanicsWhereIsolateFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tmp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustIsolate did not panic on a root that cannot hold the temp dir")
		}
	}()
	MustIsolate(root)
}

// ratchet: test_removed TestCeilingList_NamesEachDirOnceAndSkipsEmpty: moved to internal/gitenv, which owns CeilingList
// ratchet: test_removed TestCeilingList_AddsTheResolvedSpellingOfASymlinkedDir: moved to internal/gitenv, which owns CeilingList

// Isolate changes nothing about the process until it can finish: a root that
// cannot hold the run's directories leaves the git variables as they were.
func TestIsolate_ARootThatCannotHoldItsDirsLeavesTheGitVariablesAlone(t *testing.T) {
	before := map[string]string{}
	for _, name := range []string{"GIT_CEILING_DIRECTORIES", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_COUNT"} {
		before[name] = os.Getenv(name)
		if before[name] == "" {
			t.Fatalf("setup: %s is not set in an isolated run", name)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tmp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", "/inherited/.git")

	if _, err := Isolate(root); err == nil {
		t.Fatal("Isolate answered no error")
	}

	for name, want := range before {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s = %q after a failed Isolate, want %q", name, got, want)
		}
	}
	if got := os.Getenv("GIT_DIR"); got != "/inherited/.git" {
		t.Errorf("GIT_DIR = %q after a failed Isolate, want it left alone", got)
	}
}

// The probe passes in a run Main has isolated; VerifyNoLeak proves that the
// same probe fails in one that is not.
func TestProbe_PassesInAnIsolatedRun(t *testing.T) {
	t.Setenv(ProbeEnv, "1")
	ran := false
	t.Run("probe", func(t *testing.T) {
		defer func() { ran = !t.Skipped() }()
		Probe(t)
	})
	if !ran {
		t.Error("Probe skipped although the probe variable is set")
	}
}

// Everything a leaked fixture writes to the repository around a run shows in the
// state VerifyNoLeak compares: config, HEAD, the index and the refs.
func TestVictimState_ChangesWithEachThingALeakedFixtureWrites(t *testing.T) {
	cases := []struct {
		name string
		leak func(t *testing.T, victim string)
	}{
		{"config", func(t *testing.T, victim string) { gitIn(t, victim, "config", "leak.key", "1") }},
		{"HEAD", func(t *testing.T, victim string) { gitIn(t, victim, "symbolic-ref", "HEAD", "refs/heads/elsewhere") }},
		{"refs", func(t *testing.T, victim string) { gitIn(t, victim, "branch", "feat/x") }},
		{"index", func(t *testing.T, victim string) {
			if err := os.WriteFile(filepath.Join(victim, "new.txt"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, victim, "add", "new.txt")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			victim := makeVictim(t)
			before := victimState(victim)
			if again := victimState(victim); again != before {
				t.Fatal("setup: the state of an untouched repository is not stable")
			}

			c.leak(t, victim)

			if victimState(victim) == before {
				t.Errorf("a leaked %s did not change the state", c.name)
			}
		})
	}
}

// gitIn runs git in dir with the process's git variables dropped.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = cleanedEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// The probe's environment carries what the scenario needs and nothing that
// would leak the real one: a hook's variables in one scenario, a TMPDIR inside
// the victim in the other, and in both a home that is not the operator's.
func TestHostileEnv_CarriesTheScenarioAndDropsWhatNamesTheRealWorld(t *testing.T) {
	home, victim := t.TempDir(), t.TempDir()
	for name, value := range map[string]string{
		"GIT_CEILING_DIRECTORIES": "/keep/out", "GIT_CONFIG_NOSYSTEM": "1", "HOME": "/real/home", "USERPROFILE": "/real/home",
		"TMPDIR": "/real/tmp", "TMP": "/real/tmp", "TEMP": "/real/tmp", "GOTMPDIR": "/real/tmp",
		"XDG_CONFIG_HOME": "/real/xdg", "APPDATA": "/real/appdata", "LOCALAPPDATA": "/real/local", "KEEP_ME": "kept",
	} {
		t.Setenv(name, value)
	}

	hook := hostileEnv(home, victim, true)
	walk := hostileEnv(home, victim, false)

	for label, env := range map[string][]string{"hook": hook, "walk": walk} {
		got := envMap(env)
		for _, name := range []string{"GIT_CEILING_DIRECTORIES", "GIT_CONFIG_NOSYSTEM", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
			if v, ok := got[name]; ok {
				t.Errorf("%s: %s = %q survived", label, name, v)
			}
		}
		if got["KEEP_ME"] != "kept" {
			t.Errorf("%s: an unrelated variable was dropped", label)
		}
		if got[ProbeEnv] != "1" || got["HOME"] != home || got["USERPROFILE"] != home {
			t.Errorf("%s: probe %q home %q profile %q, want 1 and %q", label, got[ProbeEnv], got["HOME"], got["USERPROFILE"], home)
		}
	}
	h := envMap(hook)
	if h["GIT_DIR"] != filepath.Join(victim, ".git") || h["GIT_INDEX_FILE"] != filepath.Join(victim, ".git", "index") ||
		h["GIT_CONFIG_GLOBAL"] != filepath.Join(home, ".gitconfig") {
		t.Errorf("the hook environment lacks the repository and global config: %v", h)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR"} {
		if v, ok := h[name]; ok {
			t.Errorf("hook: %s = %q, want it dropped", name, v)
		}
	}
	w := envMap(walk)
	tmp := filepath.Join(victim, "tmp")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if w[name] != tmp {
			t.Errorf("walk: %s = %q, want %q inside the victim", name, w[name], tmp)
		}
	}
	for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_CONFIG_GLOBAL", "GOTMPDIR"} {
		if v, ok := w[name]; ok {
			t.Errorf("walk: %s = %q, want it absent", name, v)
		}
	}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			m[name] = value
		}
	}
	return m
}

// A leaked commit must run no hook of the repo it lands in, and write no gate
// state under the operator's config dir: both are named by the isolation.
func TestIsolate_SendsHooksAndTheGateStateToTheRunsRoot(t *testing.T) {
	root := filepath.Dir(os.Getenv("HOME"))
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v == "" || !inside(root, v) {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want a path under the run's root %q", v, root)
	}
	if os.Getenv("GIT_CONFIG_KEY_3") != "core.hooksPath" || !inside(root, os.Getenv("GIT_CONFIG_VALUE_3")) {
		t.Errorf("core.hooksPath = %q = %q, want an empty dir under the run's root", os.Getenv("GIT_CONFIG_KEY_3"), os.Getenv("GIT_CONFIG_VALUE_3"))
	}
}

// A go.work the box or the runner carries must not reach the throwaway modules
// a test builds or lists: go refuses a module its workspace does not name.
func TestIsolate_SwitchesTheGoWorkspaceOff(t *testing.T) {
	if got := os.Getenv("GOWORK"); got != "off" {
		t.Errorf("GOWORK = %q, want off", got)
	}
}
