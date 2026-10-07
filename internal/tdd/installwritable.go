package tdd

import "os"

// InstallWritable probes dir for write access by creating and removing a
// throwaway file — the exact permission a build into that directory needs —
// rather than reading permission bits, which POSIX ACLs and Windows ACEs can
// both override in ways os.FileMode never reflects. `aphrollo update`
// resolves --bin through os.Executable, which on a box whose binary is a
// root-owned symlink chain follows it straight into
// a directory only root owns
// pipeline's own account owns — this is what lets update find that out
// before spending a fetch and a full build on it.
func InstallWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".aphrollo-writable-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
