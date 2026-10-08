package precommit

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Builders reported vitest processes left running after the gate gave up on
// them, ended by hand. vitest is a launcher: it starts workers of its own, and
// a worker holding an open handle outlives a vitest that is killed alone. The
// merge gate ends the whole tree at its deadline; this is the proof, over a
// fake vitest that starts a grandchild and then hangs, run through the real
// SuiteRunner. Each half holds a connection open, so the read at this end
// returns the moment that process is gone and waits on no clock of its own.
const fakeHangingVitest = `import net from 'node:net'
import { spawn } from 'node:child_process'
const [host, port] = process.env.FAKE_TREE_ADDR.split(':')
function hold(role) {
  const s = net.connect(Number(port), host, () => s.write(role + '\n'))
  setInterval(() => {}, 1 << 30)
}
if (process.env.FAKE_TREE_ROLE === 'worker') {
  hold('worker')
} else {
  spawn(process.execPath, [process.argv[1]], { env: { ...process.env, FAKE_TREE_ROLE: 'worker' }, stdio: 'ignore' })
  hold('vitest')
}
`

// Serial: sets the process-wide env var FAKE_TREE_ADDR and FAKE_VITEST_LOG (makeSvelteKitRepo).
func TestMechanical_ATimedOutVitestLeavesNoWorkerRunning(t *testing.T) {
	requireNode(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("FAKE_TREE_ADDR", listener.Addr().String())
	repo, app, _ := makeSvelteKitRepo(t)
	write(t, app, filepath.Join("node_modules", "vitest", "vitest.mjs"), fakeHangingVitest)
	stageComponentChange(t, repo, app)

	arrivals := make(chan struct {
		role string
		conn net.Conn
	}, 2)
	go func() {
		for range 2 {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			role, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				c.Close()
				return
			}
			arrivals <- struct {
				role string
				conn net.Conn
			}{strings.TrimSpace(role), c}
		}
	}()

	res := Mechanical(repo, RunSuite(4*time.Second))
	if !res.Blocked || !strings.Contains(res.Message, "did not finish") {
		t.Fatalf("a hung vitest must refuse the merge as a timeout, got blocked=%v %q", res.Blocked, res.Message)
	}
	var worker net.Conn
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for range 2 {
		select {
		case got := <-arrivals:
			defer got.conn.Close()
			if got.role == "worker" {
				worker = got.conn
			}
		case <-ctx.Done():
			t.Fatal("the fake vitest never reported both halves of its tree")
		}
	}
	if worker == nil {
		t.Fatal("the fake vitest never started its worker")
	}
	if err := worker.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Read(make([]byte, 1)); err == nil || os.IsTimeout(err) {
		t.Error("the vitest worker outlived the timed-out gate run: a hung test pool is left holding the box")
	}
}
