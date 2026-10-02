package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/rollback"
)

// updateNow is the clock the pin and the install record read. A var so a test
// can state the instant.
var updateNow = time.Now

// buildClock is the instant buildAphrollo stamps into the binary as its build
// time. A var so update can freeze it for the run (pinBuildClock): the record it
// writes beside the binary then carries the very stamp the linker put in it.
var buildClock = time.Now

// pinBuildClock freezes buildClock at the instant it reads now and returns that
// instant, with the function that puts the live clock back.
func pinBuildClock() (at time.Time, restore func()) {
	prev := buildClock
	at = prev()
	buildClock = func() time.Time { return at }
	return at, func() { buildClock = prev }
}

// updateRun is one update past its checks: the commit it installs, the record
// beside the binaries, and where it reports.
type updateRun struct {
	bin          string
	remoteBranch string
	target       updateTarget
	installs     *rollback.Installs
	stdout       io.Writer
	stderr       io.Writer
}

// refLabel is what the install record says the binary was installed for.
func (r *updateRun) refLabel() string {
	if r.target.Ref != "" {
		return r.target.Ref
	}
	return r.remoteBranch
}

// install puts the target's binary in place: a kept copy built from the same
// commit when one is on the box and its bytes are the recorded ones, a fresh
// build otherwise.
func (r *updateRun) install(runGit gitFn) int {
	if kept, ok := r.installs.ByCommit(r.target.Commit, filepath.Base(r.bin)); ok {
		return r.switchToKept(kept)
	}
	return r.buildAndSwap(runGit)
}

// switchToKept makes a copy update kept the installed binary, with no build:
// the swap discipline is the same, with the copy as the staged binary.
func (r *updateRun) switchToKept(kept rollback.Binary) int {
	staged := filepath.Join(filepath.Dir(r.bin), kept.File)
	fmt.Fprintf(r.stdout, "aphrollo update: switch %s <- kept copy %s (no build needed)\n", r.target.describe(), staged)
	kept.Ref = r.refLabel()
	return r.swap(staged, kept, "kept")
}

// buildAndSwap builds the target's commit in a detached worktree, never the
// working tree, and swaps the result in.
func (r *updateRun) buildAndSwap(runGit gitFn) int {
	tmp, err := os.MkdirTemp("", "aphrollo-update-*")
	if err != nil {
		fmt.Fprintf(r.stderr, "aphrollo update: %v\n", err)
		return 1
	}
	if _, err := runGit("worktree", "add", "--detach", tmp, r.target.Commit); err != nil {
		fmt.Fprintf(r.stderr, "aphrollo update: %v\n", err)
		_ = os.RemoveAll(tmp)
		return 1
	}
	defer func() {
		_, _ = runGit("worktree", "remove", "--force", tmp)
		_ = os.RemoveAll(tmp)
	}()

	staged := siblingPath(r.bin, ".new")
	_ = os.Remove(staged)
	builtAt, restore := pinBuildClock()
	desc, err := buildAphrollo(tmp, staged)
	restore()
	if err != nil {
		fmt.Fprintf(r.stderr, "aphrollo update: build failed, nothing was replaced\n%v\n", err)
		return 1
	}
	fmt.Fprintf(r.stdout, "aphrollo update: build  %s -> %s\n", desc, staged)
	incoming := rollback.Binary{Commit: r.target.Commit, BuiltAt: builtAt.UTC().Format(time.RFC3339), Ref: r.refLabel()}
	return r.swap(staged, incoming, "build")
}

// swap replaces the installed binary with staged and records it: the outgoing
// binary's record follows its copy, the incoming one takes the installed name.
// how is "build" or "kept", for the event.
func (r *updateRun) swap(staged string, incoming rollback.Binary, how string) int {
	outgoing, hadOutgoing := outgoingRecord(r.installs, r.bin)
	stale, err := swapBinary("aphrollo update", r.bin, staged, r.stdout)
	if err != nil {
		fmt.Fprintf(r.stderr, "aphrollo update: %v\n", err)
		return 1
	}
	sum, err := rollback.FileSHA256(r.bin)
	if err != nil {
		fmt.Fprintf(r.stderr, "aphrollo update: %v\n", err)
		return 1
	}
	incoming.File = filepath.Base(r.bin)
	incoming.SHA256 = sum
	incoming.InstalledAt = updateNow().UTC().Format(time.RFC3339)
	if hadOutgoing {
		outgoing.File = filepath.Base(stale)
		r.installs.Put(outgoing)
	}
	r.installs.Put(incoming)
	r.installs.DropMissing()
	if err := r.installs.Save(); err != nil {
		fmt.Fprintf(r.stderr, "aphrollo update: the swap stands, but the record of the kept binaries was NOT written: %v\n", err)
		return 1
	}
	rollback.Emit("swap", "ok", map[string]string{"how": how, "from": outgoing.Commit, "to": incoming.Commit, "ref": incoming.Ref})
	return 0
}

// outgoingRecord is what is known of the binary about to be replaced: its
// record when the bytes still match it, else the stamp the running process
// carries when this IS that binary. ok is false when nothing names it, and an
// unnamed binary is not recorded: a guess would mislabel the copy a rollback
// later trusts.
func outgoingRecord(in *rollback.Installs, bin string) (rollback.Binary, bool) {
	sum, err := rollback.FileSHA256(bin)
	if err != nil {
		return rollback.Binary{}, false
	}
	if rec, ok := in.Get(filepath.Base(bin)); ok && rec.SHA256 == sum {
		return rec, true
	}
	if commit, builtAt, stamped := buildinfo.Stamp(); stamped && isRunningBinary(bin) {
		return rollback.Binary{Commit: commit, BuiltAt: builtAt, SHA256: sum}, true
	}
	return rollback.Binary{}, false
}

// isRunningBinary reports whether bin is the file this process was started from.
func isRunningBinary(bin string) bool {
	exe, err := execPathFn()
	if err != nil {
		return false
	}
	a, errA := os.Stat(bin)
	b, errB := os.Stat(exe)
	return errA == nil && errB == nil && os.SameFile(a, b)
}
