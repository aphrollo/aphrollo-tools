//go:build !windows

package run

import (
	"os"
	"os/exec"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// groupTree guards a child through its own process group: the child leads one
// and its children inherit it, so a signal to the group reaches the tree.
// Heavy and light children are guarded the same way, and a heavy one also
// outlives neither its parent's death (dieWithParent) nor a SIGINT or SIGTERM
// sent to this process, which liveGroups carries to its group.
type groupTree struct {
	pid   int
	heavy bool
}

func prepare(cmd *exec.Cmd, heavy bool, _ int64) (tree, error) {
	cmd.SysProcAttr = proc.TreeAttrs()
	if heavy {
		dieWithParent(cmd.SysProcAttr)
	}
	return &groupTree{heavy: heavy}, nil
}

func (g *groupTree) attach(p *os.Process) error {
	g.pid = p.Pid
	if g.heavy {
		liveGroups.add(g.pid)
	}
	return nil
}

// kill signals the group; a group with no process left answers ESRCH, which
// is the goal. A tree never attached has no group to signal, and pid 0 would
// be this process's own.
func (g *groupTree) kill() {
	if g.pid > 0 {
		_ = proc.KillTree(g.pid)
	}
}

func (g *groupTree) finish() {
	g.kill()
	if g.heavy && g.pid > 0 {
		liveGroups.remove(g.pid)
	}
}

// peak is 0: a process group has no memory measure of its own.
func (g *groupTree) peak() uint64 { return 0 }
