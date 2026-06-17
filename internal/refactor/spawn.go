package refactor

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// Spawn launches the language server for lang and returns a Conn over its stdio
// plus a cleanup func that tears the process down.
func Spawn(ctx context.Context, lang Language) (*lsp.Conn, func(), error) {
	cmd := exec.CommandContext(ctx, lang.Command, lang.Args...)
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
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		conn.Wait() // join the read-loop goroutine: Kill closed stdout, so it has seen EOF
	}
	return conn, cleanup, nil
}
