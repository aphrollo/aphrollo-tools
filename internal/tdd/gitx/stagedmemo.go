package gitx

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
)

// The commit gate asks for the staged set from half a dozen stages, with the
// index, HEAD and any merge in progress the same for all of them. The answer is
// kept per repository and replaced when the index it was read from changes.

var stagedMemo = &stagedKept{}

type stagedKept struct {
	mu     sync.Mutex
	byRepo map[string]stagedAnswer
}

type stagedAnswer struct {
	stamp   string
	files   []string
	renames map[string]string
}

func (k *stagedKept) get(repo, stamp string) ([]string, map[string]string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	a, ok := k.byRepo[repo]
	if !ok || a.stamp != stamp {
		return nil, nil, false
	}
	return slices.Clone(a.files), maps.Clone(a.renames), true
}

func (k *stagedKept) put(repo, stamp string, files []string, renames map[string]string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.byRepo == nil {
		k.byRepo = map[string]stagedAnswer{}
	}
	k.byRepo[repo] = stagedAnswer{stamp: stamp, files: slices.Clone(files), renames: maps.Clone(renames)}
}

// indexStamp names the state the staged set is a function of: the index the
// call reads (the one a running commit names, else the worktree's own), HEAD,
// the merge in progress, the reflog action that names a merge and, during a
// merge, the commits trunk names. "" when any
// of it cannot be read, which keeps nothing.
func indexStamp(repoRoot string) string {
	c := HookClient(repoRoot)
	if c == nil {
		return ""
	}
	idx := gitenv.HookIndex(repoRoot)
	if idx == "" {
		idx = filepath.Join(c.GitDir(), "index")
	}
	raw, err := os.ReadFile(idx)
	if err != nil {
		return ""
	}
	head, err := c.Head()
	if err != nil {
		return ""
	}
	sum := sha256.New()
	sum.Write(raw)
	merge := c.MergeInProgress()
	mergeText, _ := os.ReadFile(filepath.Join(c.GitDir(), merge))
	parts := []string{idx, head.SHA, merge, string(mergeText), os.Getenv(reflogActionEnv)}
	if merge != "" {
		// A merge in progress is diffed against the incoming tip once trunk
		// holds it (trunkSyncTip), so where trunk stands is part of the answer.
		for _, ref := range trunkRefs(TrunkBranch(repoRoot)) {
			parts = append(parts, ref, c.Rev(ref))
		}
	}
	for _, part := range parts {
		sum.Write([]byte{0})
		sum.Write([]byte(part))
	}
	return hex.EncodeToString(sum.Sum(nil))
}
