package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// The queue dir shadows `cargo` and `git`. On Windows that shadow used to be a
// batch file, and a batch file cannot forward an argument list: cmd.exe strips
// `^` (so `git rev-parse MERGE_HEAD^{tree}` and nextest's `-E test(/^mod::/)`
// arrived mangled) and re-splits anything quoted (which broke `gh` calls made
// through it). A COPY of the aphrollo binary under the tool's own name has no
// interpreter between the caller and the process: the copy receives the argv
// verbatim and dispatches on the name it was invoked under.

// ShimExeResult reports one install: the shim names whose bytes were written
// or refreshed, and the ones that could not be replaced because a copy is
// running. A locked shim is REPORTED, never fatal — the old copy keeps working
// and the next init refreshes it.
type ShimExeResult struct {
	Installed []string
	Locked    []string
}

// cmdShimNames are the retired batch shims. init deletes exactly these two
// names; any other .cmd in the queue dir belongs to someone else.
var cmdShimNames = []string{"cargo.cmd", "git.cmd"}

// shimExeNames are the executable copies to install. Only Windows resolves a
// command by extension, and only there does an `.exe` shadow the real tool; a
// POSIX box uses the extensionless sh shims written beside them, so it gets no
// copies and pays no bytes for them.
func shimExeNames() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	return []string{"cargo.exe", "git.exe"}
}

// InstallShimExes copies exe into dir under every shim name this platform
// needs, refreshing a copy whose size or mtime no longer matches the source.
func InstallShimExes(dir, exe string) (ShimExeResult, error) {
	return installShimExes(dir, exe, shimExeNames())
}

// RemoveCmdShims deletes the retired batch shims from dir, returning the names
// it removed so init can report each removal once. A missing file is not an
// error: the common case is that they are already gone.
func RemoveCmdShims(dir string) ([]string, error) {
	var removed []string
	for _, name := range cmdShimNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("removing %s: %w", path, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// installShimExes is InstallShimExes with the names supplied, so the copy
// mechanics are exercised on every platform rather than only where they ship.
func installShimExes(dir, exe string, names []string) (ShimExeResult, error) {
	var res ShimExeResult
	if len(names) == 0 {
		return res, nil
	}
	src, err := os.Stat(exe)
	if err != nil {
		return res, fmt.Errorf("reading %s: %w", exe, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	for _, name := range names {
		dst := filepath.Join(dir, name)
		if sameFileContent(src, dst) {
			continue
		}
		if err := replaceExe(dst, exe, src.ModTime()); err != nil {
			res.Locked = append(res.Locked, name)
			continue
		}
		res.Installed = append(res.Installed, name)
	}
	return res, nil
}

// sameFileContent reports whether dst already is the source binary, judged by
// size and mtime. The copy carries the source's mtime (replaceExe sets it), so
// a rebuilt binary — which always gets a fresh mtime — never reads as current.
func sameFileContent(src os.FileInfo, dst string) bool {
	fi, err := os.Stat(dst)
	if err != nil {
		return false
	}
	return fi.Size() == src.Size() && fi.ModTime().Equal(src.ModTime())
}

// replaceExe publishes exe at dst by writing a temp copy and renaming over it.
// Windows refuses to WRITE a running image but allows RENAMING one aside, so a
// failed rename retries with the old copy moved out of the way; only when that
// also fails is the shim reported locked.
func replaceExe(dst, exe string, mtime time.Time) error {
	tmp := dst + ".new-" + strconv.Itoa(os.Getpid())
	if err := copyFile(exe, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chtimes(tmp, mtime, mtime); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err == nil {
		sweepAside(dst)
		return nil
	}
	aside := dst + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.Rename(dst, aside); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		// Put the displaced copy back: a shim that vanished is worse than a
		// stale one, because the tool it shadows disappears from PATH.
		_ = os.Rename(aside, dst)
		os.Remove(tmp)
		return err
	}
	sweepAside(dst)
	return nil
}

// sweepAside deletes the displaced copies replaceExe left behind once the
// processes holding them exit. Best-effort: one still running simply stays for
// the next init to clear.
func sweepAside(dst string) {
	matches, err := filepath.Glob(dst + ".old-*")
	if err != nil {
		return
	}
	for _, m := range matches {
		os.Remove(m)
	}
}

// copyFile writes src's bytes to dst, executable.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
