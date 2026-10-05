package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A gate test that reaches the operator's own home is not a test failure, it
// is damage: this package's tests install hooks, write settings.json and
// append to gate.log, and every one of those has a real counterpart under
// ~/.claude and ~/.config/git/hooks. One run of `gate init` with a defaulted
// hooks dir rewrote this box's live pre-commit hook to point at a temp
// binary, which was invisible until the next commit failed.
//
// So the package redirects HOME, USERPROFILE and XDG_CONFIG_HOME at a
// temp home for its whole run, and the guard below fails if anything a test
// would write still resolves into the real one.

// gateConfigDir gives ONE test its own gate state dir. The package-wide dir
// TestMain installs is shared by every test in the binary, so two tests that
// both append to gate.log or stamp a session read each other's writes.
func gateConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("TRELLIS_DATA", t.TempDir())
	return dir
}

func TestPackageIsolation_NothingResolvesIntoTheOperatorsHome(t *testing.T) {
	if realHomeAtStart == "" {
		t.Skip("no home dir on this box, so nothing to protect") // skip-ok: there is no real home to guard
	}
	claude := filepath.Join(realHomeAtStart, ".claude")
	config := filepath.Join(realHomeAtStart, ".config")
	localShare := filepath.Join(realHomeAtStart, ".local", "share")

	check := func(when string) {
		for name, pair := range map[string][2]string{
			"gate state dir":    {tdd.StateDir(), claude},
			"claude config dir": {defaultClaudeDir(), claude},
			"git hooks dir":     {defaultGitHooksDir(), config},
			"cargo shim dir":    {defaultCargoShimDir(filepath.Join(realHomeAtStart, "bin", "aphrollo")), localShare},
		} {
			if pathIsUnder(pair[0], pair[1]) {
				t.Errorf("%s (%s) resolves into the operator's %s — %s", name, pair[0], pair[1], when)
			}
		}
	}
	check("with the package's own config dir set")
	// The fallback matters more than the happy path: it is what a test that
	// clears the variable, or forgets it, actually gets.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	check("with CLAUDE_CONFIG_DIR and XDG_CONFIG_HOME cleared")
}

func TestGateConfigDir_GivesTheTestItsOwnStateDir(t *testing.T) {
	dir := gateConfigDir(t)
	state := tdd.StateDir()
	if !pathIsUnder(state, dir) {
		t.Fatalf("state dir = %q, want it under this test's own %q", state, dir)
	}
	if entries, err := os.ReadDir(state); err == nil && len(entries) > 0 {
		t.Fatalf("a fresh config dir must carry no other test's state, found %d entries", len(entries))
	}
}

// pathIsUnder reports whether p is root or lives inside it, comparing the way
// the OS resolves paths.
func pathIsUnder(p, root string) bool {
	clean := func(s string) string {
		if abs, err := filepath.Abs(s); err == nil {
			s = abs
		}
		s = filepath.Clean(s)
		if runtime.GOOS == "windows" {
			s = strings.ToLower(s)
		}
		return s
	}
	rel, err := filepath.Rel(clean(root), clean(p))
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != ".." && !strings.HasPrefix(rel, "../")
}

// TestConfigDirIsSetThroughOneHelper keeps the isolation single-sourced: a
// test that sets CLAUDE_CONFIG_DIR by hand can point it anywhere, and the
// next reader has to check each site rather than one.
// isolationPkgDir is the package directory, read before any test can chdir. It is
// not taken from runtime.Caller: the gate builds with -trimpath, which makes the
// caller's file a module path, not a place on disk.
var isolationPkgDir = func() string { d, _ := os.Getwd(); return d }()

func TestConfigDirIsSetThroughOneHelper(t *testing.T) {
	const marker = `t.Setenv("CLAUDE_CONFIG_DIR"`
	owners := map[string]bool{"isolation_test.go": true}
	// The package dir from THIS file's own path, not the process cwd: a test
	// that ran before this one may have t.Chdir'd somewhere else.
	pkgDir := isolationPkgDir
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") || owners[name] {
			continue
		}
		body, err := os.ReadFile(filepath.Join(pkgDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), marker) {
			t.Errorf("%s sets CLAUDE_CONFIG_DIR directly — call gateConfigDir(t) instead", name)
		}
	}
}

// A hook waits 150 ms for its shadow record; a test on a loaded box must not.
// Set at package load, before TestMain runs anything, so no test sees the
// hook's own budget. See TestPackageIsolation_ShadowRecordsAreNotCutAtTheHooksBudget.
func init() { shadow.Budget = time.Minute }

// A shadow record is built inside shadow.Budget, 150 ms in a hook. A test that
// runs the hook on a loaded box, or with git behind a slow queue, outruns that:
// the record is then an unjudged "budget" event instead of the one the test
// reads, and the goroutine that was left behind writes its record later, into
// whichever test's event root is current. The package's tests judge what the
// records say, not how fast the box is, so the budget is a minute here.
func TestPackageIsolation_ShadowRecordsAreNotCutAtTheHooksBudget(t *testing.T) {
	if shadow.Budget < 10*time.Second {
		t.Fatalf("shadow.Budget = %s in this package's tests: a loaded box turns a shadow record into a budget skip and strays the build into the next test", shadow.Budget)
	}
}
