package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
)

// Both of the gate's locks used to be keyed under os.TempDir(), which is
// PER USER on every platform this runs on. That made them locks in name only
// the moment a box had two accounts on it: a second user (or a CI runner)
// building the same target dir took a private lock and both builds wrote the
// same build directory, and each account got its own full set of global slots,
// so the box ran 2N concurrent builds under a governor sized for N.
//
// So the two locks now live where their scope actually is: the per-target lock
// beside the target dir it guards, and the global slots in a machine-wide
// directory every account can write.

// sharedLockFileMode is the permission a lock file is created with. Every
// account that builds into a shared target dir has to be able to OPEN the
// lock; one that cannot reads the lock as held and never builds again.
const sharedLockFileMode = 0o666

// sharedLockDirName is the machine-wide directory holding the global build
// slots. Resolved once per process: the answer cannot change while it runs,
// and the probe below is a create/delete that must not sit in a poll loop.
var sharedLockDirName = sync.OnceValue(resolveSharedLockDir)

// sharedLockDir is the machine-wide lock directory, or the per-user temp dir
// when no shared candidate is writable (announced once — a box in that state
// cannot serialise builds across accounts, and that is worth saying out loud).
func sharedLockDir() string { return sharedLockDirName() }

func resolveSharedLockDir() string {
	for _, dir := range sharedLockCandidates() {
		if err := ensureSharedDir(dir); err == nil {
			return dir
		}
	}
	fmt.Fprintf(os.Stderr, "gate: no machine-wide lock dir is writable; build slots fall back to %s and serialise per user only\n", os.TempDir())
	return os.TempDir()
}

// sharedLockCandidates are the machine-wide directories to try, in order.
// %ProgramData% on Windows and /var/tmp elsewhere are the two locations every
// account can write and nothing sweeps out from under a running build.
func sharedLockCandidates() []string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return []string{filepath.Join(pd, "aphrollo", "locks")}
		}
		return nil
	}
	return []string{filepath.Join("/var/tmp", "aphrollo-locks")}
}

// ensureSharedDir creates dir world-writable and proves this process can
// actually put a file in it. MkdirAll's mode goes through the umask, so the
// permissions are set again explicitly; the sticky bit keeps one account from
// deleting another's lock file, the same contract /tmp itself carries.
func ensureSharedDir(dir string) error {
	if err := ensureSharedSubdir(dir); err != nil {
		return err
	}
	probe := filepath.Join(dir, ".probe-"+strconv.Itoa(os.Getpid()))
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return err
	}
	f.Close()
	os.Remove(probe)
	return nil
}

// ensureSharedSubdir creates dir (and its parents) with the shared lock
// directory's own mode: world-writable and sticky. MkdirAll's mode goes
// through the umask, which leaves 0775 — a directory the next account can
// neither lock nor record itself in. The chmod is best-effort because a
// directory another account created is not ours to re-mode.
func ensureSharedSubdir(dir string) error {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(dir); err == nil && fi.Mode()&(os.ModePerm|os.ModeSticky) != 0o777|os.ModeSticky {
			_ = os.Chmod(dir, 0o777|os.ModeSticky)
		}
	}
	return nil
}

// writeSharedRecord writes a holder or waiter record beside a shared lock
// with the lock file's own mode. Every account that shares the lock has to be
// able to READ the record, or it cannot name the holder; and it has to be able
// to REPLACE it, because the sticky lock dir lets no account delete a record
// another one left behind, so truncating in place is the only overwrite there
// is. The mode is set again explicitly because the create goes through the
// umask.
func writeSharedRecord(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, sharedLockFileMode)
	if err != nil {
		return err
	}
	if fi, serr := f.Stat(); serr == nil && fi.Mode().Perm() != sharedLockFileMode {
		_ = f.Chmod(sharedLockFileMode)
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// lockLitterDirs are the directories the sweep looks in for stale owner
// records and test stub dirs: where the locks live now, and the per-user temp
// dir where they lived before the move — 871 files were measured there, and a
// sweep pointed only at the new home abandons every one of them.
// A test's override is the one isolation seam this package has, so under an
// override the sweep looks THERE and nowhere else: a scan that reached into
// the real temp dir from inside a test would report the box's litter as the
// test's own.
func lockLitterDirs() []string {
	dirs := []string{lockDir()}
	if lockDirOverridden() {
		return dirs
	}
	if tmp := os.TempDir(); tmp != dirs[0] {
		dirs = append(dirs, tmp)
	}
	return dirs
}

// sharedTargetLockPath is the per-target lock's home: a .aphrollo directory
// inside the target dir itself. Shared by construction — every process that
// builds into that directory, under any account, names the same file.
func sharedTargetLockPath(targetDir string) string {
	return filepath.Join(targetDir, ".aphrollo", "build.lock")
}
