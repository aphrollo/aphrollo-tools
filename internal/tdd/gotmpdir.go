package tdd

import (
	"os"
	"path/filepath"
)

// goTmpDirName is the repo-local directory a `go test`/`go build`/`go vet`
// child writes its scratch into: unset GOTMPDIR left 133 go-build* survivors
// in the OS temp dir (one killed run apiece), six of them Defender-quarantined
// as tdd.test.exe (Behavior:Win32/DefenseEvasion.A!ml). Landing it inside the
// project root means a lane's scratch dies with `git worktree remove` — no
// sweep, no accumulation — instead of outliving the worktree that made it.
const goTmpDirName = ".aphrollo-gotmp"

// goTmpDir is the scratch directory itself, rooted at the go command's own
// working directory (root for a plain runner, r.Dir for a resolved one — see
// runnerDir) rather than some other project boundary, so it always lands
// exactly where `git worktree remove` will remove it from.
func goTmpDir(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, goTmpDirName)
}

// goTmpEnv is the temp-dir variables a `go` child needs, all pointing at the
// same repo-local directory. GOTMPDIR is what the go tool itself reads when
// it stages a compiled test binary before running it — the one that was
// unset. TMPDIR, TMP and TEMP are named alongside it for the same reason
// mutantsTempEnv (mutants_disk.go) names all three rather than one: they are
// what a POSIX or Windows os.TempDir() call reads, so a test under this run
// that shells out to some other tool, or calls t.TempDir()/os.TempDir()
// itself, lands its own scratch here too rather than back in the OS temp
// dir — setting only one of these is the bug that already cost a cargo-mutants
// run its disk.
func goTmpEnv(dir string) []string {
	tmp := goTmpDir(dir)
	if tmp == "" {
		return nil
	}
	_ = os.MkdirAll(tmp, 0o755)
	return []string{"GOTMPDIR=" + tmp, "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp}
}
