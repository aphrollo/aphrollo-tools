package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	return dir
}

func TestPackageIsolation_NothingResolvesIntoTheOperatorsHome(t *testing.T) {
	if realHomeAtStart == "" {
		t.Skip("no home dir on this box, so nothing to protect") // skip-ok: there is no real home to guard
	}
	claude := filepath.Join(realHomeAtStart, ".claude")
	config := filepath.Join(realHomeAtStart, ".config")

	check := func(when string) {
		for name, pair := range map[string][2]string{
			"gate state dir":    {tdd.StateDir(), claude},
			"claude config dir": {defaultClaudeDir(), claude},
			"git hooks dir":     {defaultGitHooksDir(), config},
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
func TestConfigDirIsSetThroughOneHelper(t *testing.T) {
	const marker = `t.Setenv("CLAUDE_CONFIG_DIR"`
	owners := map[string]bool{"isolation_test.go": true}
	// The package dir from THIS file's own path, not the process cwd: a test
	// that ran before this one may have t.Chdir'd somewhere else.
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	pkgDir := filepath.Dir(self)
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
