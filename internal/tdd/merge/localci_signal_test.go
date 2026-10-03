package merge

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A signal ends the process from the handler goroutine, which never lets Run
// return to remove its scratch directory: the handler has to cancel the run
// and remove that scratch itself.
func TestLocalCI_ASignalMidRunCancelsTheRunAndRemovesItsScratchBeforeExit(t *testing.T) {
	root, mark := ciLane(t, `on: pull_request
jobs:
  hold:
    steps:
      - run: |
          echo "$GOPATH" > "$LOCALCI_MARK"
          timeout 20 tail -f /dev/null
`)
	injected := make(chan os.Signal, 1)
	origSource := prGateSignalChan
	prGateSignalChan = func() (chan os.Signal, func()) { return injected, func() { signal.Stop(injected) } }
	t.Cleanup(func() { prGateSignalChan = origSource })

	scratchGone := make(chan bool, 1)
	origExit := prGateSignalExit
	prGateSignalExit = func(int) {
		scratch := scratchOf(readFileString(t, mark))
		_, err := os.Stat(scratch)
		scratchGone <- scratch != "" && os.IsNotExist(err)
	}
	t.Cleanup(func() { prGateSignalExit = origExit })

	go func() {
		wait, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()
		for scratchOf(readFileStringOrEmpty(mark)) == "" {
			select {
			case <-wait.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		injected <- syscall.SIGTERM
	}()

	_, _ = LocalCI(root, nil)

	select {
	case gone := <-scratchGone:
		if !gone {
			t.Error("the run's scratch directory was still there when the signal handler exited the process")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the signal never reached the handler")
	}
}

// scratchOf is the run's scratch directory a step printed its GOPATH under
// (scratch/isolation/gopath), "" while nothing is printed.
func scratchOf(gopath string) string {
	gopath = strings.TrimSpace(gopath)
	if !strings.Contains(gopath, "isolation") {
		return ""
	}
	return filepath.Dir(filepath.Dir(gopath))
}

func readFileStringOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
