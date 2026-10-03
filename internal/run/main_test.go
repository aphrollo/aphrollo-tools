package run

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"time"

	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// helperEnv names the mode this test binary plays when a test starts it as a
// child: the children these tests supervise are the test binary itself, so
// they need nothing installed and behave the same on every OS. Every such
// child is started with noTests first, so a child that loses its mode (a
// production change that drops the environment) runs no tests and exits,
// instead of starting the whole suite again from inside itself.
const (
	helperEnv = "RUN_TEST_HELPER"
	noTests   = "-test.run=^$"
)

// TestMain runs a helper mode when one is asked for, and otherwise cuts the
// package's run off from the box's git world. See gitiso.Isolate.
func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(helper(mode, os.Args[2:]))
	}
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}

// helper is one child behaviour. args are what the test put after the binary.
func helper(mode string, args []string) int {
	switch mode {
	case "sleep":
		block()
	case "exit3":
		return 3
	case "env":
		for _, name := range args {
			if v, ok := os.LookupEnv(name); ok {
				fmt.Printf("%s=%s\n", name, v)
			}
		}
	case "chain", "leave":
		// Starts a sleeping grandchild, records its own pid and the grandchild's
		// in the file args[0], then sleeps ("chain") or exits at once ("leave"),
		// leaving the grandchild running.
		child := exec.Command(os.Args[0], noTests)
		child.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := child.Start(); err != nil {
			return 4
		}
		line := strconv.Itoa(os.Getpid()) + "\n" + strconv.Itoa(child.Process.Pid) + "\n"
		if err := os.WriteFile(args[0], []byte(line), 0o644); err != nil {
			return 5
		}
		if mode == "chain" {
			block()
		}
	case "alloc":
		mb, _ := strconv.Atoi(args[0])
		block := make([]byte, mb<<20)
		for i := 0; i < len(block); i += 4096 {
			block[i] = 1
		}
		fmt.Println("allocated", len(block)>>20)
	}
	return 0
}

// block holds the helper until a supervisor ends it. The watchdog is a bound
// no test outlives, so a helper a failed test leaked still exits by itself.
func block() {
	time.AfterFunc(10*time.Minute, func() { os.Exit(9) })
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	select {}
}
