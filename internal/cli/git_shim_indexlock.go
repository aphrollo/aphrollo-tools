package cli

import (
	"fmt"
	"os"
	"time"
)

// defaultGitIndexLockGrace is how long index.lock must sit UNCHANGED before
// the shim stops waiting on it (issue #608). A killed git leaves a zero-byte
// index.lock behind that nothing holds, and the shim used to poll it for the
// whole 20-minute budget before giving up with "holder: (unknown)" -- twenty
// minutes of dead checkout over a file whose removal took effect instantly.
//
// #565's rule for a build lock ("a lock whose holder is provably gone loses
// its protection") cannot be applied here: git's lock files record no pid, so
// there is nothing to ask the OS about, and a lock a live git is holding is
// byte-for-byte the same file as one a killed git left. So the lock KEEPS its
// protection -- the shim never removes it -- and what changes is only how long
// the shim is willing to say nothing: seconds, then the file and the remedy,
// rather than twenty silent minutes. 15s is well past any write git does to
// the file in one go, and short enough that a stale lock costs a retry rather
// than a session.
const defaultGitIndexLockGrace = 15 * time.Second

// indexLockPresent reports whether path exists -- best-effort, any stat
// error (including "not found") reads as absent. An empty path is "no index
// lock to wait for" (a repo-scoped verb).
func indexLockPresent(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// gitIndexLockWatch tracks ONE index.lock across the shim's poll loop: the
// signature of the last state seen, and the moment that state was first
// seen. now is the clock, injectable so a test owns it instead of sleeping.
type gitIndexLockWatch struct {
	now       func() time.Time
	since     time.Time
	signature string
}

// stallLine reports whether the wait should end because index.lock has not
// changed for the whole grace period, and the diagnostic to print if so.
// blocked is whether index.lock is what this invocation is actually waiting
// on: when another shim holds the advisory lock the holder is known and named
// already, and index.lock is merely that holder's, so the window is dropped.
// Every look that finds the file changed, or gone, drops it too -- only an
// unbroken run of identical observations spanning grace ends the wait.
func (w *gitIndexLockWatch) stallLine(path string, grace time.Duration, blocked bool) (string, bool) {
	if grace <= 0 {
		grace = defaultGitIndexLockGrace
	}
	now := time.Now
	if w.now != nil {
		now = w.now
	}
	info, err := os.Stat(path)
	if !blocked || err != nil {
		w.since, w.signature = time.Time{}, ""
		return "", false
	}
	sig := fmt.Sprintf("%d/%d", info.Size(), info.ModTime().UnixNano())
	if sig != w.signature {
		w.since, w.signature = now(), sig
		return "", false
	}
	unchanged := now().Sub(w.since)
	if unchanged < grace {
		return "", false
	}
	return gitIndexLockStallDiagnostic(path, info, unchanged, now()), true
}

// gitIndexLockStallDiagnostic is what the operator gets instead of twenty
// silent minutes: the exact file, how long it has been untouched, how old it
// is, and the one command that clears it -- guarded by the check only a human
// can make, since the shim cannot tell a held lock from an abandoned one.
func gitIndexLockStallDiagnostic(path string, info os.FileInfo, unchanged time.Duration, now time.Time) string {
	return fmt.Sprintf(
		"git: stopped waiting for %s: unchanged for %s, last written %s ago (%d bytes)\n"+
			"git: a git lock file records no pid, so a lock a live git holds and one a killed git left behind are the same file -- this one is never removed for you\n"+
			"git: if no git process is working in this checkout, clear it and retry:  rm %s",
		path,
		formatMinSec(unchanged),
		formatMinSec(now.Sub(info.ModTime())),
		info.Size(),
		path,
	)
}
