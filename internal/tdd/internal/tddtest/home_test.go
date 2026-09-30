package tddtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// inside reports whether path is dir or under it, ignoring case on Windows.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// A test that resolves the operator's home, config or data directory writes
// into the operator's real profile. Main isolates the package's run, so every
// variable those lookups read must already name a directory under the run's
// temp home: on Windows that means USERPROFILE and APPDATA, not just HOME.
func TestMain_EveryHomeVariableNamesADirectoryUnderTheTempRoot(t *testing.T) {
	root := filepath.Dir(os.Getenv("CLAUDE_CONFIG_DIR"))
	for _, name := range homeVars {
		v := os.Getenv(name)
		if v == "" || !inside(root, v) {
			t.Errorf("%s = %q, want a path under the run's temp root %q", name, v, root)
		}
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "GIT_CONFIG_GLOBAL", "CARGO_HOME"} {
		if v := os.Getenv(name); !inside(root, v) {
			t.Errorf("%s = %q, want a path under the run's temp root %q", name, v, root)
		}
	}
	if os.Getenv("GIT_CONFIG_NOSYSTEM") != "1" {
		t.Errorf("GIT_CONFIG_NOSYSTEM = %q, want 1: git would read the machine's system config", os.Getenv("GIT_CONFIG_NOSYSTEM"))
	}
}

// The Windows resolvers, simulated on any host: whichever variable the
// platform's UserHomeDir/UserConfigDir reads must resolve inside the temp home.
func TestMain_TheStdlibHomeLookupsResolveInsideTheTempRoot(t *testing.T) {
	root := filepath.Dir(os.Getenv("CLAUDE_CONFIG_DIR"))
	home, err := os.UserHomeDir()
	if err != nil || !inside(root, home) {
		t.Errorf("os.UserHomeDir() = %q, %v; want a path under %q", home, err, root)
	}
	cfg, err := os.UserConfigDir()
	if err != nil || !inside(root, cfg) {
		t.Errorf("os.UserConfigDir() = %q, %v; want a path under %q", cfg, err, root)
	}
	cache, err := os.UserCacheDir()
	if err != nil || !inside(root, cache) {
		t.Errorf("os.UserCacheDir() = %q, %v; want a path under %q", cache, err, root)
	}
}

// Moving the home must not move the toolchain: with the caches pinned to where
// they resolved before the move, no `go` a test spawns rebuilds the world.
func TestPinToolchainHomes_KeepsTheGoCachesOutOfTheTempHome(t *testing.T) {
	root := filepath.Dir(os.Getenv("CLAUDE_CONFIG_DIR"))
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

// The runaway of #997 in miniature: this binary, started as if it were the
// gate (`<pkg>.test gate ...`), must refuse instead of running the suite. The
// child sets the probe variable so that, were the refusal missing, the suite
// it runs skips this test rather than starting another child.
func TestMain_ATestBinaryStartedAsTheGateRefusesWithoutRunningTheSuite(t *testing.T) {
	if os.Getenv("TDDTEST_REEXEC_PROBE") == "1" {
		t.Skip("running as the probe's child: the refusal is missing if this line is reached")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, code := runBounded(t, exe, []string{"gate", "runphase", "--job", "x"}, []string{"TDDTEST_REEXEC_PROBE=1"})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output:\n%s", code, out)
	}
	if !strings.Contains(out, "refusing to run the test suite") {
		t.Fatalf("output does not say why it refused:\n%s", out)
	}
	if strings.Contains(out, "PASS") || strings.Contains(out, "--- ") {
		t.Fatalf("the child ran tests instead of refusing:\n%s", out)
	}
}

func TestReexecGuard_RefusesOnlyWhatProcRefuses(t *testing.T) {
	var sb strings.Builder
	if code, stop := reexecGuard([]string{"x.test", "-test.v"}, &sb); stop || code != 0 || sb.Len() != 0 {
		t.Fatalf("flags-only argv was refused: code=%d stop=%v out=%q", code, stop, sb.String())
	}
	msg, _ := proc.RefuseTestReexec([]string{"x.test", "gate"})
	if code, stop := reexecGuard([]string{"x.test", "gate"}, &sb); !stop || code != 2 || strings.TrimSpace(sb.String()) != msg {
		t.Fatalf("positional argv: code=%d stop=%v out=%q, want 2, true, %q", code, stop, sb.String(), msg)
	}
}

// runBounded runs exe with args and extra environment, bounded by a deadline,
// and returns its combined output and exit code.
func runBounded(t *testing.T, exe string, args, env []string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("running %s %v: %v", exe, args, err)
	return "", -1
}

// A commit in a fixture repo must never leave git working behind it. After
// every commit git runs `git maintenance run --auto --detach`, and when a
// task is due that forks a background process which repacks and prunes the
// repo after `git commit` has already returned. The test's t.TempDir cleanup
// then removes the repo while that process still writes into it, and fails
// with "directory not empty" (#1004). The run's own environment therefore
// switches auto maintenance off, above any config a fixture repo carries.
func TestMain_GitNeverRunsMaintenanceInTheBackground(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "maintenance.auto", "true"},
		{"config", "gc.auto", "1"},
		{"config", "gc.autoDetach", "true"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for key, want := range map[string]string{
		"maintenance.auto": "false",
		"gc.auto":          "0",
		"gc.autoDetach":    "false",
	} {
		cmd := exec.Command("git", "config", "--get", key)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git config --get %s: %v", key, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("git config %s = %q, want %q", key, got, want)
		}
	}
}
