package gitx

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// When the OS cannot give a hook another thread or process (Windows errno
// 1450, EAGAIN), starting git fails. The hook must say so in one line and go on
// without git's answer, not dump a trace or repeat itself per call (#997).
func TestOutputGit_SaysOnceThatTheOSCannotStartAProcess(t *testing.T) {
	var out strings.Builder
	restore := setExhaustionNotice(&out)
	defer restore()
	exhausted := &exec.Error{Name: "git", Err: syscall.EAGAIN}

	noteIfExhausted(exhausted)
	noteIfExhausted(exhausted)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "cannot start a process") {
		t.Fatalf("notice = %q, want exactly one line saying the OS cannot start a process", out.String())
	}
}

func TestOutputGit_IsSilentAboutOtherFailures(t *testing.T) {
	var out strings.Builder
	restore := setExhaustionNotice(&out)
	defer restore()

	noteIfExhausted(errors.New("exit status 128"))
	noteIfExhausted(nil)

	if out.Len() != 0 {
		t.Fatalf("a non-exhaustion error produced output: %q", out.String())
	}
}

// A hook that fans out git calls without bound is how one process holds
// hundreds of children; the cap makes the next caller wait for a slot.
func TestOutputGit_WaitsForASlotWhenTheCapIsReached(t *testing.T) {
	for i := 0; i < cap(gitSlots); i++ {
		gitSlots <- struct{}{}
	}
	released := false
	release := func() {
		if !released {
			released = true
			for i := 0; i < cap(gitSlots); i++ {
				<-gitSlots
			}
		}
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := outputGit(exec.Command(gitBinary(), "--version"))
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("git ran with every slot taken (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("git --version after the slots freed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("git never ran after the slots freed")
	}
}

func TestOutputGit_ReturnsSlotsItTook(t *testing.T) {
	before := len(gitSlots)
	if _, err := outputGit(exec.Command(gitBinary(), "--version")); err != nil {
		t.Fatal(err)
	}
	if after := len(gitSlots); after != before {
		t.Fatalf("slots held %d -> %d: a finished git kept its slot", before, after)
	}
}
