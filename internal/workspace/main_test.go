package workspace

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// stubDirs holds every temp dir a package-lifetime fixture built (a compiled
// stub binary shared by every test through a `sync.OnceValues`), so TestMain
// can remove them all once the whole package's run is over — that lifetime is
// the fixture's, not any one test's, so a per-test t.Cleanup would pull the
// stub out from under the rest. See internal/cli's own copy of this pattern.
var (
	stubDirsMu sync.Mutex
	stubDirs   []string
)

func registerStubDir(dir string) {
	stubDirsMu.Lock()
	defer stubDirsMu.Unlock()
	stubDirs = append(stubDirs, dir)
}

// TestMain isolates the package's run from the operator's machine.
//
// A REFUSING gh goes in front of the real one: this package shells out for
// `gh pr create`, `gh pr merge`, `gh pr edit` and `gh api`, and nothing but
// each test's own arrangement stood between a mutated guard and a real pull
// request being merged. The same exposure in internal/tdd filed three real
// issues against this repo from nobody (#155, #196, #197). See ghnet_test.go.
//
// HOME is redirected for the same class of reason: the prepare step runs
// `git config --global --add safe.directory <dir>`, which was writing a real
// person's ~/.gitconfig on every run. On the shared self-hosted runner two
// concurrent jobs wrote that one file at once and both died with
// `Apply: step 2 (mark git-safe) failed: exit status 255`, on a tip whose
// local gate had run the same suite green. See home_isolation_test.go.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ws-")
	if err != nil {
		panic(err)
	}
	realHomeAtStart, _ = os.UserHomeDir()
	gitiso.MustIsolate(dir)
	leaveTheBoxQueue()
	ghRefusalPath = installRefusingGh()
	// The vast majority of this package's tests stub the individual gh seams
	// (ghViewPR, ghCreatePR, …) directly and never arrange a working `gh` on
	// PATH — the REFUSING gh above sees to that. requireGH's own real
	// implementation would refuse every one of them before Apply even
	// reaches its stubbed seam, so it defaults to a no-op here; the tests
	// that exercise requireGH itself rebind it explicitly.
	requireGH = func() error { return nil }
	code := m.Run()
	if ghRefusalPath != "" {
		os.RemoveAll(ghRefusalPath)
	}
	stubDirsMu.Lock()
	dirs := append([]string(nil), stubDirs...)
	stubDirsMu.Unlock()
	for _, d := range dirs {
		os.RemoveAll(d)
		if _, err := os.Stat(d); err == nil {
			fmt.Fprintf(os.Stderr, "workspace: registered stub dir %s survived cleanup\n", d)
			if code == 0 {
				code = 1
			}
		}
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// realHomeAtStart is the operator's own home, recorded before it is replaced,
// so a test can prove the replacement happened.
var realHomeAtStart string
