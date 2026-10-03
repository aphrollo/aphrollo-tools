package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

// A heavy child that starts children of its own must leave none of them
// behind when it is closed, times out, or exits: on Windows a test binary
// stuck in kernel exit held 6 GB of commit for hours, and MSYS grandchildren
// outlived `taskkill /T`. These tests are that proof, over a chain the test
// binary builds itself and over the MSYS bash -> bash -> sleep chain.

// chain is a process tree to start: how, and how many distinct pids it
// records in the pid file once it is up.
type chain struct {
	name string
	pids int
	spec func(t *testing.T, pidFile string, leave bool) Spec
}

func chains() []chain {
	return []chain{
		{"go child and grandchild", 2, func(t *testing.T, pidFile string, leave bool) Spec {
			mode := "chain"
			if leave {
				mode = "leave"
			}
			return helperSpec(t, mode, pidFile)
		}},
		{"bash, bash, sleep", 3, func(t *testing.T, pidFile string, leave bool) Spec {
			bash := bashCommand()
			if bash == "" {
				t.Skip("no bash on this box") // skip-ok: the chain needs a real shell, which a box may lack.
			}
			script := filepath.Join(t.TempDir(), "chain.sh")
			if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
				t.Fatal(err)
			}
			hold := "wait"
			if leave {
				hold = "leave"
			}
			return Spec{Name: bash, Args: []string{filepath.ToSlash(script), filepath.ToSlash(pidFile), hold}, Env: os.Environ(), Area: t.TempDir()}
		}},
	}
}

// watch records pids of the tree for cleanup, so a failing test leaves none.
func watch(t *testing.T, pids []int) {
	t.Helper()
	t.Cleanup(func() {
		for _, pid := range pids {
			forceKill(pid)
		}
	})
}

func assertTreeGone(t *testing.T, pids []int) {
	t.Helper()
	waitFor(t, "the whole process tree to be gone", func() bool {
		for _, pid := range pids {
			if alive(pid) {
				return false
			}
		}
		return true
	})
}

func TestHeavy_CloseEndsTheWholeTree(t *testing.T) {
	for _, ch := range chains() {
		t.Run(ch.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pids")
			c, err := StartHeavy(context.Background(), ch.spec(t, pidFile, false))
			if err != nil {
				t.Fatal(err)
			}
			pids := []int{c.Pid()}
			watch(t, pids)
			waitFor(t, "the tree to start", func() bool { return len(readPids(pidFile)) >= ch.pids })
			pids = append(pids, readPids(pidFile)...)
			watch(t, pids)
			for _, pid := range pids {
				if !alive(pid) {
					t.Fatalf("pid %d of the tree is not running before the Close, so the test proves nothing", pid)
				}
			}

			closeWithin(t, c)

			assertTreeGone(t, pids)
		})
	}
}

func TestHeavy_TimeoutEndsTheWholeTree(t *testing.T) {
	for _, ch := range chains() {
		t.Run(ch.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pids")
			spec := ch.spec(t, pidFile, false)
			spec.Timeout = 6 * time.Second
			c, err := StartHeavy(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			watch(t, []int{c.Pid()})

			if err := waitWithin(t, c); !errors.Is(err, ErrTimeout) {
				t.Fatalf("Wait = %v, want ErrTimeout", err)
			}

			pids := readPids(pidFile)
			if len(pids) < ch.pids {
				t.Fatalf("the tree recorded %d pids before the timeout, want %d: it never came up", len(pids), ch.pids)
			}
			pids = append(pids, c.Pid())
			watch(t, pids)
			assertTreeGone(t, pids)
		})
	}
}

func TestHeavy_WaitEndsWhatTheChildLeftBehind(t *testing.T) {
	for _, ch := range chains() {
		t.Run(ch.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pids")
			c, err := StartHeavy(context.Background(), ch.spec(t, pidFile, true))
			if err != nil {
				t.Fatal(err)
			}
			watch(t, []int{c.Pid()})

			if err := c.Wait(); err != nil {
				t.Fatalf("Wait = %v: the child was meant to exit cleanly", err)
			}

			pids := readPids(pidFile)
			watch(t, pids)
			if len(pids) < ch.pids {
				t.Fatalf("the tree recorded %d pids, want %d", len(pids), ch.pids)
			}
			assertTreeGone(t, pids)
		})
	}
}

// The helpers of the proof live in runtest, where the packages that start
// their children through run reach them too.
var (
	alive       = runtest.Alive
	bashCommand = runtest.BashCommand
	readPids    = runtest.ReadPids
	forceKill   = runtest.ForceKill
)
