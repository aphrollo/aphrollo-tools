package merge

import (
	"os"
	"os/exec"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// The per-function test maps the commit-time mutation run selects tests with
// (internal/tdd/mutation/mutants_testmap.go) are rebuilt after a merge lands,
// because a merge is what changes what the next lane's tests are selected
// from. The build is a compile and a solo run of every test of every package
// that has any, so it runs detached, in the background, and never on the
// path of the merge or of a commit. Only a repo that declared
// mutants-at-commit is touched.

// testMapSpawnFn starts the background build for the repo at root. A seam so
// no test starts the real detached process.
var testMapSpawnFn = spawnTestMapBuild

// SetTestMapSpawnForTest replaces the background spawn and answers the
// restore.
func SetTestMapSpawnForTest(fn func(root string)) (restore func()) {
	prev := testMapSpawnFn
	testMapSpawnFn = fn
	return func() { testMapSpawnFn = prev }
}

// testMapBuildWanted reports whether the repo at root declares
// mutants-at-commit. A config the mutation reader refuses wants nothing: the
// merge gate says so loudly, and a hook that fires in every repo on the box
// stays out of it.
func testMapBuildWanted(root string) bool {
	cfg, err := ReadMutantsConfig(root)
	return err == nil && cfg.AtCommit
}

// startTestMapBuild starts the background build in a repo that wants it, and
// does nothing anywhere else.
func startTestMapBuild(root string) {
	if testMapBuildWanted(root) {
		testMapSpawnFn(root)
	}
}

// spawnTestMapBuild runs `aphrollo gate mutants testmap` detached in root, so
// it outlives the hook that started it. It refuses to start from a Go test
// binary, which would answer the verb by running its whole suite. A build that
// cannot start leaves the last maps in place, which stay usable.
func spawnTestMapBuild(root string) {
	self, err := proc.SpawnableSelf(os.Executable, os.Environ())
	if err != nil {
		return
	}
	cmd := exec.Command(self, "gate", "mutants", "testmap")
	cmd.Dir = root
	cmd.Env = proc.ChildEnv(os.Environ(), append(os.Environ(), "CI=1", "NO_COLOR=1"))
	closeStdio := silentStdio(cmd)
	cmd.SysProcAttr = detachedAttrs()
	err = cmd.Start()
	closeStdio()
	if err != nil {
		return
	}
	_ = cmd.Process.Release()
}
