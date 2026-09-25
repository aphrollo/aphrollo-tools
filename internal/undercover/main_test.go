package undercover

import (
	"os"
	"testing"
)

// TestMain drops the repo-pointing variables a git hook exports: run from
// inside one, the fixtures' own `git init` and `git config` would otherwise
// act on the real repository instead of their temp dirs.
func TestMain(m *testing.M) {
	for _, k := range []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX"} {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
