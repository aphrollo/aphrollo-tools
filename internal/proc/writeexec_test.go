package proc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A fork that lands while another goroutine holds a script open for writing
// hands the child a copy of that write descriptor, and until the child execs
// the script cannot be executed: execve answers ETXTBSY and a shell exits 126.
// A fork holds syscall.ForkLock for writing, so a write that holds it for
// reading cannot overlap one. The proof is that the write waits while a fork
// would be in progress, and completes once it is not.
func TestWriteExecutable_WaitsForAForkInProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool")

	syscall.ForkLock.Lock()
	done := make(chan error, 1)
	go func() { done <- WriteExecutable(path, []byte("#!/bin/sh\nexit 0\n"), 0o755) }()

	select {
	case err := <-done:
		syscall.ForkLock.Unlock()
		t.Fatalf("WriteExecutable finished (err=%v) while a fork held ForkLock, so its write descriptor can leak into that child", err)
	case <-time.After(200 * time.Millisecond):
	}
	syscall.ForkLock.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("WriteExecutable did not finish after the fork released ForkLock")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "#!/bin/sh\nexit 0\n" {
		t.Errorf("wrote %q", body)
	}
}
