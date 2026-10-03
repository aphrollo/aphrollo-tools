package gitx

import (
	"fmt"
	"sync"

	igit "github.com/aphrollo/aphrollo-tools/internal/git"
)

// A hook that judges one edit asks git the same questions from a dozen places:
// where the worktree is, what HEAD is, what is dirty. Each used to be its own
// spawn. The hook now holds one git.Client per worktree, which reads the
// answers that sit in files from those files and asks git only for the dirty
// set, once per hook (BeginHook starts the batch that one status answers).

var hookGit = struct {
	sync.Mutex
	gen     uint64
	clients map[string]*igit.Client
}{clients: map[string]*igit.Client{}}

// BeginHook starts a new batch of questions: a status read before it is never
// answered again after it. The edit hooks call it once on entry; a long-lived
// caller that never does would keep reading one status.
func BeginHook() {
	hookGit.Lock()
	hookGit.gen++
	hookGit.Unlock()
}

// HookClient is the client for the worktree dir sits in, the same one for
// every directory of that worktree, and nil when dir is in no repository. It
// runs the git this package execs, never a queue shim, marked as already
// queued.
func HookClient(dir string) *igit.Client {
	if dir == "" {
		return nil
	}
	bin := gitBinary()
	c, err := igit.New(dir, igit.Options{Bin: bin, Env: []string{GitQueuedEnv + "=1"}})
	if err != nil {
		return nil
	}
	key := c.Root() + "\x00" + bin
	hookGit.Lock()
	defer hookGit.Unlock()
	if known, ok := hookGit.clients[key]; ok {
		return known
	}
	hookGit.clients[key] = c
	return c
}

// HookStatus is the dirty set of the worktree dir sits in for the hook's
// batch: one spawn the first time it is asked in a batch, the same answer
// after. Both results are nil when dir is in no repository or git cannot say.
func HookStatus(dir string) (*igit.Client, *igit.Status) {
	c := HookClient(dir)
	if c == nil {
		return nil, nil
	}
	hookGit.Lock()
	key := fmt.Sprintf("hook-%d", hookGit.gen)
	hookGit.Unlock()
	st, err := c.Status(key)
	if err != nil {
		return c, nil
	}
	return c, st
}

// HookRoot is the top directory of the worktree dir sits in, read from the
// `.git` files without a spawn, "" when dir is in no repository. It is spelled as
// RepoRoot spells it, the one canonical spelling (git.Canonical).
func HookRoot(dir string) string {
	c := HookClient(dir)
	if c == nil {
		return ""
	}
	return igit.Canonical(c.Root())
}

// FreshStatus is HookStatus for a caller that asks twice to learn whether the
// tree moved between: every call reads git again.
func FreshStatus(dir string) (*igit.Client, *igit.Status) {
	c := HookClient(dir)
	if c == nil {
		return nil, nil
	}
	st, err := c.Status("")
	if err != nil {
		return c, nil
	}
	return c, st
}
