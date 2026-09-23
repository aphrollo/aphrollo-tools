package tdd

import (
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
)

// indexTree is the tree the staged index would commit as.
//
// It runs with GIT_INDEX_FILE HONOURED, unlike every other git call the gate
// makes. `git commit -a` and `git commit -- <paths>` build a TEMPORARY index
// and point the hooks at it through that variable; reading `.git/index`
// instead names a different tree, and does it while the commit holds
// `.git/index.lock`, so the call can fail outright. Either way the stamp
// never matches and no commit made that way is ever gated in CI's eyes.
//
// `git write-tree` WRITES tree objects into the object database, which is
// exactly what the commit is about to do anyway, so it adds no garbage a
// commit would not.
func indexTree(repoRoot string) string {
	cmd := exec.Command(gitBinary(), "write-tree")
	cmd.Dir = repoRoot
	env := cleanGitEnv()
	if idx := os.Getenv("GIT_INDEX_FILE"); idx != "" {
		env = append(env, "GIT_INDEX_FILE="+idx)
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// suiteRanGreen records that a root group's suite actually RAN and passed
// during this gate process. A cache hit is deliberately not that: it says the
// identical tree was proven earlier, which is a fine reason to skip a rerun
// and a poor basis for a claim CI will weigh its own red against. It is also
// what makes an amend note-free — an amend re-runs the gate, hits the cache,
// and proves nothing new.
var suiteRanGreen atomic.Bool
