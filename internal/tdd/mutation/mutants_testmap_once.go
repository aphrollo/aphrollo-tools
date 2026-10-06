package mutation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// testMapLockPath is the lock one test-map build of a repository holds. It
// lives in the repository's shared git directory, so every worktree of the
// repository contends for the same one; a directory outside any repository
// keeps it in the directory itself.
func testMapLockPath(root string) string {
	dir := gitCommonDir(root)
	if dir == "" {
		dir = root
	}
	return filepath.Join(dir, "aphrollo-testmap.lock")
}

// testMapOnce runs build unless another build of the repository at root is
// running. A request that finds one running leaves a mark and returns at once;
// the running build sees the mark when it ends and runs one more pass, so any
// number of requests during a pass cost exactly one more pass, never a build
// each (#1248).
func testMapOnce(root string, build func() int, out io.Writer) int {
	lock := testMapLockPath(root)
	again := lock + ".again"
	code := 0
	for first := true; ; first = false {
		release, ok := TryAcquireFileLock(lock)
		if !ok {
			if first {
				// The mark is best effort: a request whose mark cannot be
				// written is one pass the maps miss until the next merge, and
				// the maps in place stay usable meanwhile.
				_ = os.WriteFile(again, nil, 0o644)
				fmt.Fprintln(out, "test maps: a build of this repository is already running; it runs once more when it ends")
			}
			return code
		}
		// Removing the mark before the pass starts means a request during
		// the pass leaves a fresh one; a missing mark is not an error.
		_ = os.Remove(again)
		code = build()
		release()
		if _, err := os.Stat(again); err != nil {
			return code
		}
	}
}
