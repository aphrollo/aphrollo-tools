package tdd

import (
	"fmt"
	"time"
)

// Split out of posttooluse.go (module_size, issue #430): the advisory
// strings for a run the SuiteRunner could not get a verdict from — timeout,
// timeout-streak backoff, and the machine-wide build-lock skip.

// timeoutAdvisory composes the one-line advisory for a run the SuiteRunner
// killed at its deadline: a timeout says nothing about the code, but staying
// silent about it reads as "green" to whoever is watching — this makes the
// inconclusive explicit instead. Points at `gate status` (issue #430) rather
// than leaving a rerun as the only visible move: a rerun queues behind
// whatever is holding the box, which `gate status` names instead of a
// session discovering it three refused reruns later.
func timeoutAdvisory(r Runner, root string, dur time.Duration) string {
	return fmt.Sprintf("gate: %s in %s → TIMEOUT after %ds — inconclusive, code NOT tested; a rerun queues behind whatever is holding the box — see: aphrollo gate status",
		cmdString(r), root, int(dur.Seconds()+0.5))
}

// streakSkipAdvisory composes the one-line advisory for the timeout-streak
// backoff: the suite was not even invoked this edit, which is a DIFFERENT
// fact from "invoked and inconclusive" (timeoutAdvisory) and must read as
// such.
func streakSkipAdvisory(root string) string {
	return fmt.Sprintf("gate: %s → SKIPPED (2 timeouts at this HEAD; re-armed after next commit)", root)
}

// queuedSkippedAdvisory composes the one-line advisory for the machine-wide
// cargo build-lock backoff (wired in by the buildlock acquirer, task A3):
// another cargo build already holds the box, so this edit's suite is skipped
// rather than queued behind it and blowing the edit-time budget. Names the
// holder (task A7) when the owner file is readable, and points at
// `gate status` (issue #430) for the fuller picture — every slot, not just
// the one that happened to block this edit — plus the same "a rerun queues
// behind it" caution timeoutAdvisory carries.
func queuedSkippedAdvisory(root, targetDir string) string {
	return fmt.Sprintf("gate: %s → QUEUED-SKIPPED (every build slot for %s is busy%s) — inconclusive; a rerun queues behind it too — see: aphrollo gate status",
		root, targetDir, buildLockHolderNote(targetDir))
}

// buildLockHolderNote renders a best-effort ", holder: <cmd> in <cwd>"
// clause from a holder of targetDir's build slots, or "" when no
// owner info is available — the owner file is inherently racy (it may have
// just been removed, or a build predating this feature never wrote one), so
// callers append it only when non-empty rather than claiming "unknown"
// explicitly.
func buildLockHolderNote(targetDir string) string {
	o, ok := ReadBuildSlotOwner(targetDir)
	if !ok {
		return ""
	}
	return fmt.Sprintf(", holder: %s in %s", o.Cmd, o.Cwd)
}
