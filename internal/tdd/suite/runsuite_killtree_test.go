package suite

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// exec.CommandContext kills the direct child and nothing else. `go test` is a
// launcher: it compiles a test binary and runs it as a grandchild. So a suite
// killed at its deadline left `<pkg>.test.exe` alive, holding its build
// outputs and its share of the machine — for the rest of the session, because
// nothing ever looked for it again. Measured on this box: stranded test
// binaries from earlier timed-out runs still live, one still spawning git
// children.
//
// The kill has to reach the tree. killTree already does exactly that for a
// deferred phase (taskkill /T on Windows, the process group elsewhere), and
// this wires that same kill to the suite runner's cancel.
//
// Liveness is reported over a socket rather than polled for: each half of the
// tree holds a connection open, and the read at the other end returns the
// moment that process is gone, whether that is immediately or once the
// scheduler gets to it.
func TestRunSuite_KillsTheGrandchildOfATimedOutSuite(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	spawner, err := treeFixtureBinary()
	if err != nil {
		t.Fatal(err)
	}

	type held struct {
		role string
		conn net.Conn
	}
	arrivals := make(chan held, 2)
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
			arrivals <- held{role: strings.TrimSpace(role), conn: c}
		}
	}()

	run := RunSuite(2 * time.Second)
	res := run(Runner{Cmd: spawner, Args: []string{"spawn", listener.Addr().String()}, Dir: dir}, dir)

	if !res.TimedOut {
		t.Fatalf("the fixture must outlive its deadline for this test to prove anything: %+v", res)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var grandchild net.Conn
	for range 2 {
		select {
		case got := <-arrivals:
			defer got.conn.Close()
			if got.role == "grandchild" {
				grandchild = got.conn
			}
		case <-ctx.Done():
			t.Fatal("the fixture never reported both halves of its tree")
		}
	}
	if grandchild == nil {
		t.Fatal("the fixture never reported a grandchild")
	}

	// A dead process's socket closes and this read returns at once; a live one
	// holds it open until the deadline, which is the defect.
	if err := grandchild.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := grandchild.Read(make([]byte, 1)); err == nil || os.IsTimeout(err) {
		t.Error("the grandchild outlived the suite that was killed at its deadline — a `go test` killed this way leaves its compiled test binary running forever")
	}
}

// treeFixtureSource is a two-role binary: `spawn <addr>` starts a copy of
// itself as `hold`, then holds a connection of its own. That is the shape
// `go test` has — a launcher whose real work runs as a grandchild of the
// process the runner holds. Neither role waits on a clock: both block on a
// read that ends only when the process does.
const treeFixtureSource = `package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
)

func hold(addr, role string) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		os.Exit(1)
	}
	fmt.Fprintln(c, role)
	c.Read(make([]byte, 1))
}

func main() {
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	if os.Args[1] == "spawn" {
		if err := exec.Command(os.Args[0], "hold", os.Args[2]).Start(); err != nil {
			os.Exit(1)
		}
		hold(os.Args[2], "parent")
		return
	}
	hold(os.Args[2], "grandchild")
}
`

// treeFixtureBinary builds that fixture once for the whole package.
var treeFixtureBinary = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aphrollo-tree-fixture")
	if err != nil {
		return "", err
	}
	tddtest.RegisterTempDir(dir)
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(treeFixtureSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module treefixture\n\ngo 1.26\n"), 0o644); err != nil {
		return "", err
	}
	name := "tree"
	if runtime.GOOS == "windows" {
		name = "tree.exe"
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the process-tree fixture: %v\n%s", err, out)
	}
	return bin, nil
})
