package tdd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A cargo artifact's freshness is judged by comparing its mtime against its
// source's: an artifact stamped AHEAD of "now" is fresh forever, however the
// source changes, and the package it belongs to is never rebuilt again. A
// merge combining two branches' target-dir history hit this for real (issue
// #276): `cargo test --no-run --package server` failed against a `movement`
// build matching neither branch, because a future-stamped rmeta from an
// earlier session's clock-skew workaround (`touch -d "+4 minutes"`, applied
// to dodge fail-first target poisoning before that got its own fix — see
// invalidateFailFirstArtifacts) never went stale. Two things made the report
// hard to diagnose, and both say this must be checked before every cargo
// invocation, not just the merge path: `cargo check` and
// `cargo test --no-run` disagreed on the identical tree seconds apart (a
// green check is not evidence the test profile will build), and clearing it
// needed a different -p set per stage (the poisoning is per-package, not
// per-command).

// futureMtimeTolerance absorbs ordinary clock/filesystem jitter (an NTP
// step, filesystem timestamp rounding) without false-firing: the reported
// skew was hours, three orders of magnitude past this.
const futureMtimeTolerance = 5 * time.Second

// futureMtimeChecked remembers which target dirs THIS PROCESS already swept.
// Nothing writes into a target dir between two stages of the same gate run
// except this same lock's own holders, so a second walk of a possibly
// enormous target dir would only re-confirm what the first one already
// answered.
var futureMtimeChecked sync.Map // map[string]bool

// invalidateFutureStampedArtifacts wipes target when ANY file inside it
// carries a future mtime. The condition is unconditionally wrong — no clock
// is ever legitimately fast — and once one artifact in a SHARED target dir
// is untrustworthy the whole dir is: cargo's freshness check can true a
// stale package against another package's fresh-looking dependency graph,
// which is exactly the "server" vs "shared" split the report describes.
// Called with the caller's (runCargoLocked's) target lock already held, so
// nothing else is writing into target while this runs.
func invalidateFutureStampedArtifacts(target string) {
	if target == "" {
		return
	}
	if _, seen := futureMtimeChecked.LoadOrStore(target, true); seen {
		return
	}
	cutoff := time.Now().Add(futureMtimeTolerance)
	var futurePath string
	var futureMTime time.Time
	_ = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if futurePath != "" {
			return filepath.SkipAll
		}
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(cutoff) {
			futurePath, futureMTime = path, info.ModTime()
			return filepath.SkipAll
		}
		return nil
	})
	if futurePath == "" {
		return
	}
	fmt.Fprintf(os.Stderr,
		"gate: %s carries a future-stamped artifact (%s, mtime %s, %s ahead of now) — the whole target dir is untrustworthy, wiping it before the build\n",
		target, futurePath, futureMTime.Format(time.RFC3339), time.Since(futureMTime).Abs())
	appendGateLog("buildlock", target, "future-mtime", "target-wiped", 0)
	os.RemoveAll(target)
}
