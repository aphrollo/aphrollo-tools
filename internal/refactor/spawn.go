package refactor

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// spawnWaitDelay bounds the teardown. Once the tree has been killed there is
// nothing left to wait for, so a Wait still blocked on an inherited pipe is
// a hang, not patience -- an `outline` that never returns is worse than one
// that leaves a pipe unread.
const spawnWaitDelay = 2 * time.Second

// Spawn launches the language server for lang and returns a Conn over its stdio
// plus a cleanup func that tears the process down.
//
// The teardown ends the whole process TREE, not the server's own pid: a
// language server is a launcher (rust-analyzer runs `cargo metadata` and
// `cargo check` underneath itself), so killing it alone left a cargo build
// compiling on a shared box, holding a target dir no sweep could reclaim.
// Both exits go through it -- an early return calling cleanup, and the
// context's own cancellation.
func Spawn(ctx context.Context, lang Language) (*lsp.Conn, func(), error) {
	cmd := exec.CommandContext(ctx, lang.Command, lang.Args...)
	cmd.SysProcAttr = proc.TreeAttrs()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return proc.KillTree(cmd.Process.Pid)
	}
	cmd.WaitDelay = spawnWaitDelay
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w (is it installed and on PATH?)", lang.Command, err)
	}

	conn := lsp.NewConn(stdin, stdout)
	cleanup := func() {
		conn.Close()
		// The tree kill comes BEFORE stdin closes. Closing stdin is how a
		// well-behaved server exits, and a server that has already exited is
		// a tree nothing can walk any more: its cargo children are reparented
		// and survive the kill aimed at a pid that is gone.
		_ = proc.KillTree(cmd.Process.Pid)
		_ = stdin.Close()
		_ = cmd.Wait()
		conn.Wait() // join the read-loop goroutine: Kill closed stdout, so it has seen EOF
	}
	return conn, cleanup, nil
}
