package refactor

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
)

// spawnWaitDelay bounds the teardown. Once the tree has been killed there is
// nothing left to wait for, so a Wait still blocked on an inherited pipe is
// a hang, not patience -- an `outline` that never returns is worse than one
// that leaves a pipe unread.
const spawnWaitDelay = 2 * time.Second

// Spawn launches the language server for lang and returns a Conn over its stdio
// plus a cleanup func that tears the process down.
//
// The server is a heavy child of internal/run, so the teardown ends the whole
// process TREE, not the server's own pid: a language server is a launcher
// (rust-analyzer runs `cargo metadata` and `cargo check` underneath itself),
// so killing it alone left a cargo build compiling on a shared box, holding a
// target dir no sweep could reclaim. Both exits go through it -- an early
// return calling cleanup, and the context's own cancellation.
func Spawn(ctx context.Context, lang Language) (*lsp.Conn, func(), error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW)
		return nil, nil, err
	}
	child, err := childrun.StartHeavy(ctx, childrun.Spec{
		Name: lang.Command, Args: lang.Args, EnvAsIs: true,
		Stdin: stdinR, Stdout: stdoutW, PipeGrace: spawnWaitDelay,
	})
	// The child holds its ends of the pipes now, or never will.
	closeAll(stdinR, stdoutW)
	if err != nil {
		closeAll(stdinW, stdoutR)
		return nil, nil, fmt.Errorf("start %s: %w (is it installed and on PATH?)", lang.Command, err)
	}
	stopWatch := child.CloseOnDone(ctx)

	conn := lsp.NewConn(stdinW, stdoutR)
	cleanup := func() {
		conn.Close()
		// The tree kill comes BEFORE stdin closes. Closing stdin is how a
		// well-behaved server exits, and a server that has already exited is
		// a tree nothing can walk any more: its cargo children are reparented
		// and survive the kill aimed at a pid that is gone.
		child.Close()
		stopWatch()
		_ = stdinW.Close()
		conn.Wait() // join the read-loop goroutine: the kill closed stdout, so it has seen EOF
		_ = stdoutR.Close()
	}
	return conn, cleanup, nil
}

// closeAll closes files whose close error says nothing the caller could act on.
func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
