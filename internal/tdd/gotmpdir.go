package tdd

import (
	"os"
	"path/filepath"
)

// goTmpDirName is the leaf GoTmpRootDir creates beside the worktrees, the
// same convention MutantsRootDir's own "mutants" leaf uses.
const goTmpDirName = "gotmp"

// GoTmpRootDir is the repo's shared go-scratch directory, resolved the same
// way MutantsRootDir resolves the mutation runner's own directory: beside
// the worktrees, keyed on the PRIMARY checkout, never nested inside dir.
//
// dir is routinely a LANE worktree, and `.git` is one of rootMarkers
// (runner.go): landing the scratch dir inside dir meant every t.TempDir()
// fixture a `go test` child created under it inherited the worktree as its
// own project root, even though the test asked for none — and a test that
// then wrote through that resolved root wrote into the real tracked tree
// (issue #532). Resolving beside the worktrees instead
// (`<parent-of-primary>/.worktrees/<repo>/gotmp`) keeps no `.git` above it
// while still landing in the project area, where `git worktree remove`
// leaves nothing else to sweep.
//
// "" when dir's primary checkout cannot be resolved — refuse rather than
// fall back to a path inside dir, which reproduces the bug this fixes (the
// same refusal MutantsRootDir makes for the identical reason, issue #515).
func GoTmpRootDir(dir string) string {
	if dir == "" {
		return ""
	}
	primary := primaryCheckoutRoot(dir)
	if primary == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(primary), ".worktrees", filepath.Base(primary), goTmpDirName)
}

// goTmpDir is the scratch directory itself, resolved from the go command's
// own working directory (root for a plain runner, r.Dir for a resolved one —
// see runnerDir) via GoTmpRootDir rather than joined onto dir directly.
func goTmpDir(dir string) string {
	return GoTmpRootDir(dir) // see GoTmpRootDir's doc comment for the refusal shape
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
