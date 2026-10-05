package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// gitInit makes a throwaway repo with the given files (relative path → content),
// committing them so `git ls-files` sees them tracked.
func gitInit(t *testing.T, files map[string]string) string {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := fixtureGit(append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// The initialised repo TestMain built once, not an init and two configs here.
	if err := gitiso.CopyRepo(dir, emptyRepoTemplate()); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

// emptyRepoTemplate is an initialised repo with the fixture identity and no
// commit, built on first use and copied by gitInit and gitInitRepo: an init and
// two configs per test were spawns this package paid hundreds of times. The
// directory is the package run's, removed by TestMain with the stub dirs.
var emptyRepoTemplate = sync.OnceValue(func() string {
	root, err := os.MkdirTemp("", "aphrollo-cli-repo-template-")
	if err != nil {
		panic(err)
	}
	registerStubDir(root)
	repo := filepath.Join(root, "repo")
	if err := gitiso.BuildRepo(repo, nil); err != nil {
		panic(err)
	}
	return repo
})
