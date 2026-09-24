package tdd

import "os"

// InstallWritable probes dir for write access by creating and removing a
// throwaway file — the exact permission a build into that directory needs —
// rather than reading permission bits, which POSIX ACLs and Windows ACEs can
// both override in ways os.FileMode never reflects. `aphrollo update`
// resolves --bin through os.Executable, which on a box that deploys via CI
// (deploy/deploy-prod.sh) follows two symlinks straight into
// /opt/aphrollo-cli/releases/<ts>-<sha>/, a directory only the deploy
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
