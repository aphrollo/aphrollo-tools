//go:build !windows

package run

import (
	"os"
	"os/exec"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// groupTree guards a child through its own process group: the child leads one
// and its children inherit it, so a signal to the group reaches the tree.
// Heavy and light children are guarded the same way. Nothing relies on
// Pdeathsig.
type groupTree struct{ pid int }

func prepare(cmd *exec.Cmd, _ bool, _ int64) (tree, error) {
	cmd.SysProcAttr = proc.TreeAttrs()
	return &groupTree{}, nil
}

func (g *groupTree) attach(p *os.Process) error {
	g.pid = p.Pid
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

func (g *groupTree) finish() { g.kill() }
