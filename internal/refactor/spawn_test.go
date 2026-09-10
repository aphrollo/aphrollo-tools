package refactor

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// treeStubSource is a stand-in language server that does what rust-analyzer
// does: it starts a child of its own (cargo metadata / cargo check) and then
// sits on stdin. The child announces itself on a socket the test owns and
// then holds that connection open for as long as it lives, so the test
// learns of its death from the OS closing the socket rather than by asking
// any one platform how to inspect a pid. Both halves are the same binary,
// selected by argv[1].
const treeStubSource = `package main

import (
	"io"
	"net"
	"os"
	"os/exec"
)

func main() {
	addr := os.Args[2]
	if os.Args[1] == "child" {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			os.Exit(1)
		}
		if _, err := c.Write([]byte("alive\n")); err != nil {
			os.Exit(1)
		}
		// Reads nothing anybody sends: this blocks until the process is
		// killed, and the socket closes with it.
		_, _ = io.Copy(io.Discard, c)
		return
	}
	child := exec.Command(os.Args[0], "child", addr)
	if err := child.Start(); err != nil {
		os.Exit(1)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
`

// buildTreeStub compiles treeStubSource and returns the binary's path.
func buildTreeStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(treeStubSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "treestub")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("building the process-tree stub: %v\n%s", err, out)
	}
	return bin
}

// grandchildWatch is the test's end of the stub child's socket.
type grandchildWatch struct {
	conn net.Conn
}

// watchGrandchild starts the stub server through Spawn and waits for its
// CHILD to announce itself. Returns the spawn's cleanup and the live socket.
func watchGrandchild(t *testing.T, ctx context.Context) (func(), grandchildWatch) {
	t.Helper()
	stub := buildTreeStub(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := ln.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}

	_, cleanup, err := Spawn(ctx, Language{
		Name: "stub", Command: stub, Args: []string{"parent", ln.Addr().String()},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	conn, err := ln.Accept()
	if err != nil {
		cleanup()
		t.Fatalf("setup: the stub server's child never connected, so there is nothing to prove was killed: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "alive\n" {
		cleanup()
		t.Fatalf("setup: the child announced %q (err %v), want \"alive\"", line, err)
	}
	return cleanup, grandchildWatch{conn: conn}
}

// mustDie asserts the watched child process is gone: its socket reaches
// end-of-stream, which only its death produces. A read that merely times out
// means the process is still there.
func (w grandchildWatch) mustDie(t *testing.T, how string) {
	t.Helper()
	if err := w.conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	_, err := w.conn.Read(buf[:])
	if err == nil {
		t.Fatalf("%s: the child sent data instead of dying", how)
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("%s: the language server's child is still running — the server's own pid was killed and its cargo tree was left compiling", how)
	}
}

// TestSpawn_CleanupEndsTheServersChildrenToo pins the leak. Spawn's cleanup
// killed the direct pid only, and rust-analyzer's expensive work is not the
// direct pid: it is the `cargo metadata` / `cargo check` tree underneath it.
// An outline/show/rename that returns early therefore left a cargo build
// compiling on a shared box, holding a target dir no sweep could then
// reclaim. Every other spawn site in this repo already tree-kills for
// exactly this reason.
func TestSpawn_CleanupEndsTheServersChildrenToo(t *testing.T) {
	cleanup, watch := watchGrandchild(t, context.Background())
	cleanup()
	watch.mustDie(t, "after cleanup")
}

// TestSpawn_ContextCancellationEndsTheServersChildrenToo pins the same leak
// on the other exit: exec.CommandContext's own cancellation kills the direct
// pid too, so a refactor whose context deadline expires left the identical
// orphan tree behind.
func TestSpawn_ContextCancellationEndsTheServersChildrenToo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cleanup, watch := watchGrandchild(t, ctx)
	t.Cleanup(cleanup)
	cancel()
	watch.mustDie(t, "after the context was cancelled")
}
