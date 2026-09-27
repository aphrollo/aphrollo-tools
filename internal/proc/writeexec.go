package proc

import (
	"os"
	"syscall"
)

// WriteExecutable writes a file that this process, or a child it starts, is
// about to execute. A fork that lands while the file is open for writing
// copies that descriptor into the child, and until the child execs the file
// cannot be executed: execve answers ETXTBSY and a shell running it exits 126.
// Every fork holds syscall.ForkLock for writing, so holding it for reading
// across open, write and close keeps the descriptor out of every child.
func WriteExecutable(path string, data []byte, perm os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, data, perm)
}
