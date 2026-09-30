package gitiso

import (
	"os"
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

func TestCeilingList_NamesEachDirOnceAndSkipsEmpty(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	sep := string(os.PathListSeparator)

	if got, want := ceilingList(a, "", b), a+sep+b; got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
	if got := ceilingList(""); got != "" {
		t.Errorf("ceilingList of nothing = %q, want empty", got)
	}
}

func TestCeilingList_AddsTheResolvedSpellingOfASymlinkedDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err) // skip-ok: an environment probe, symlinks need privilege on some platforms.
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}

	want := link + string(os.PathListSeparator) + resolved
	if got := ceilingList(link); got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
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
	for _, name := range append([]string{"GIT_CONFIG_GLOBAL", "TMPDIR", "TMP", "TEMP"}, homeVars...) {
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
	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "3" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want 3: auto maintenance switched off", got)
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
