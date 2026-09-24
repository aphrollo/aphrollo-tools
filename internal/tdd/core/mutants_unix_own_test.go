//go:build !windows

package core

import (
	"os"
	"os/exec"
	"testing"
)

func TestPidRunning_LiveDeadAndInvalidPids(t *testing.T) {
	if pidRunning(0) || pidRunning(-1) {
		t.Fatal("pid <= 0 must never read as running")
	}
	if !pidRunning(os.Getpid()) {
		t.Fatal("this process's own pid must read as running")
	}

	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running `true`: %v", err)
	}
	if pidRunning(cmd.Process.Pid) {
		t.Fatal("a reaped, exited process's pid must not read as running")
	}
}
