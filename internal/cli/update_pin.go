package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rollback"
)

// holdForPin answers an update that must not go on because of the pin. A pin
// written by a newer aphrollo is never read or replaced, so every form of update
// refuses. A pinned box told to follow its branch says what it is pinned to and
// stops, before any network or build: a habit of typing `aphrollo update` must
// not drop a deliberate rollback, so leaving the pin takes --unpin or --to.
func holdForPin(stdout, stderr io.Writer, state rollback.PinState, pin rollback.Pin, to string, unpin bool, remoteBranch string) (code int, held bool) {
	switch {
	case state == rollback.PinUnreadable:
		fmt.Fprintf(stderr, "aphrollo update: %s was written by a newer aphrollo; this one will not read or overwrite it — run aphrollo update from that one\n", rollback.PinPath())
		return 1, true
	case state == rollback.Pinned && to == "" && !unpin:
		fmt.Fprintf(stdout, "aphrollo update: pinned to %s; not following %s [skip]\n", pin.Describe(), remoteBranch)
		fmt.Fprintf(stdout, "aphrollo update: run aphrollo update --unpin to return to %s, or aphrollo update --to <ref> to move the pin\n", remoteBranch)
		return 0, true
	}
	return 0, false
}

// settlePin records the pin or clears it once the binary is where the run was
// asked to put it, and writes what it did to the event log. Nothing here runs
// when the swap failed, so the pin always stands for the binary on the box.
func (r *updateRun) settlePin(to string, unpin bool, state rollback.PinState, current rollback.Pin) int {
	switch {
	case to != "":
		want := rollback.Pin{Ref: r.target.Ref, Commit: r.target.Commit, At: updateNow().UTC().Format(time.RFC3339)}
		if state == rollback.Pinned && current.Ref == want.Ref && current.Commit == want.Commit {
			fmt.Fprintf(r.stdout, "aphrollo update: pin    already pinned to %s [skip]\n", want.Describe())
			return 0
		}
		if err := rollback.WritePin(want); err != nil {
			fmt.Fprintf(r.stderr, "aphrollo update: the binary is at %s, but the pin was NOT recorded: %v\n", want.Describe(), err)
			return 1
		}
		fmt.Fprintf(r.stdout, "aphrollo update: pin    pinned to %s; aphrollo update --unpin returns to %s\n", want.Describe(), r.remoteBranch)
		rollback.Emit("pin", "set", map[string]string{"ref": want.Ref, "commit": want.Commit})
	case unpin:
		cleared, err := rollback.ClearPin()
		if err != nil {
			fmt.Fprintf(r.stderr, "aphrollo update: the binary follows %s again, but the pin was NOT cleared: %v\n", r.remoteBranch, err)
			return 1
		}
		if !cleared {
			fmt.Fprintf(r.stdout, "aphrollo update: unpin  not pinned [skip]\n")
			return 0
		}
		fmt.Fprintf(r.stdout, "aphrollo update: unpin  cleared the pin on %s; following %s again\n", current.Describe(), r.remoteBranch)
		rollback.Emit("unpin", "cleared", map[string]string{"ref": current.Ref, "commit": current.Commit})
	}
	return 0
}

// dryRun is what a --dry update reports about itself.
type dryRun struct {
	repo, remote, branch, bin, to string
	unpin                         bool
	state                         rollback.PinState
	pin                           rollback.Pin
	kept                          []rollback.Binary
}

// print states the plan: the fetch, the build, the swap, the pin change and the
// binaries kept for rollback. Nothing is fetched, built, swapped or recorded.
func (d dryRun) print(stdout io.Writer) {
	fmt.Fprintf(stdout, "aphrollo update (dry run): nothing fetched, built or swapped\n")
	if d.to != "" {
		fmt.Fprintf(stdout, "  fetch: %s in %s, then resolve %s to a commit on %s/%s\n", d.remote, d.repo, d.to, d.remote, d.branch)
		fmt.Fprintf(stdout, "  build: ./cmd/aphrollo from a detached worktree at that commit, unless a kept binary was built from it\n")
	} else {
		fmt.Fprintf(stdout, "  fetch: %s/%s in %s\n", d.remote, d.branch, d.repo)
		fmt.Fprintf(stdout, "  build: ./cmd/aphrollo from a detached worktree at that ref\n")
	}
	fmt.Fprintf(stdout, "  swap:  %s\n", d.bin)
	switch {
	case d.to != "":
		fmt.Fprintf(stdout, "  pin:   would pin the box to %s (aphrollo update --unpin returns to %s/%s)\n", d.to, d.remote, d.branch)
	case d.unpin && d.state == rollback.Pinned:
		fmt.Fprintf(stdout, "  pin:   would clear the pin on %s after returning to %s/%s\n", d.pin.Describe(), d.remote, d.branch)
	case d.unpin:
		fmt.Fprintf(stdout, "  pin:   not pinned; --unpin would clear nothing\n")
	}
	for _, b := range d.kept {
		fmt.Fprintf(stdout, "  kept:  %s built %s (%s)\n", rollback.ShortSHA(b.Commit), b.BuiltAt, b.File)
	}
}
