package tdd

import (
	"os"
	"runtime"
)

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

// sharedLockFileMode is the permission a lock file is created with. Every
// account that builds into a shared target dir has to be able to OPEN the
// lock; one that cannot reads the lock as held and never builds again.
const sharedLockFileMode = 0o666
