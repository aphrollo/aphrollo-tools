package lock

import (
	"encoding/json"
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
//
// A first version wiped the whole target dir unconditionally on any hit.
// Review caught what that means on a box with REAL persistent skew (an
// unsynced VM, a container or NFS/SMB mount whose clock differs): every
// cargo invocation of every gate run would find a future-stamped artifact
// again the moment it rebuilt one, and wipe the whole cache again, forever,
// paying the full cold-rebuild cost on every run while reporting it once
// per run to a log nobody reads. The property this file now holds is
// narrower and load-bearing: a skewed box degrades ONCE per incident and
// says so, never repeatedly.

// futureMtimeTolerance absorbs ordinary clock/filesystem jitter (an NTP
// step, filesystem timestamp rounding) without false-firing: the reported
// skew was hours, three orders of magnitude past this.
const futureMtimeTolerance = 5 * time.Second

// futureMtimeLargeSkew separates a plausible clock NUDGE (an NTP step, a
// resumed VM) from a misconfigured clock, for the LOG WORDING only — the
// reported incident (#276) was itself hours of skew and still needed the
// automatic FIRST repair, so magnitude alone never skips the initial wipe.
// What it changes is the warning attached to that first repair: a skew past
// this bar says plainly that a repeat will not be repaired, priming whoever
// reads the log for the "repeat-no-wipe" line if the clock really is wrong.
const futureMtimeLargeSkew = time.Hour

// futureMtimeMarkerExpiry bounds how long a recorded repair keeps a target
// dir from being wiped again: long enough that a genuinely fixed clock's
// target dir is never wiped a second time for the SAME incident, short
// enough that a box whose clock gets fixed (and whose operator never knows
// this marker exists) is not wedged in "no wipe" state forever.
const futureMtimeMarkerExpiry = 7 * 24 * time.Hour

// futureMtimeChecked remembers which target dirs THIS PROCESS already swept.
// Nothing writes into a target dir between two stages of the same gate run
// except this same lock's own holders, so a second walk of a possibly
// enormous target dir would only re-confirm what the first one already
// answered.
var futureMtimeChecked sync.Map // map[string]bool

// futureMtimeMarker records the one wipe this guard permits per target dir
// per incident, persisted OUTSIDE target (a wipe would otherwise erase its
// own memory) so the "already repaired, still recurring" signal survives
// across the separate PROCESSES each gate run actually is.
type futureMtimeMarker struct {
	WipedAt time.Time `json:"wipedAt"`
}

// futureMtimeMarkerPath is where target's repair history lives, keyed like
// every other per-target file (targetDirKey) under the same state dir
// gate.log and the mech-cache already use. "" when stateDir() cannot be
// resolved, in which case every run is treated as a first offense — the
// same fallback gate.log itself takes when unconfigured.
func futureMtimeMarkerPath(target string) string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "future-mtime", targetDirKey(target)+".json")
}

// loadFutureMtimeMarker reads target's repair history, ok=false when there
// is none (or it cannot be read) — the caller then treats this as the FIRST
// offense.
func loadFutureMtimeMarker(target string) (futureMtimeMarker, bool) {
	path := futureMtimeMarkerPath(target)
	if path == "" {
		return futureMtimeMarker{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return futureMtimeMarker{}, false
	}
	var m futureMtimeMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return futureMtimeMarker{}, false
	}
	return m, true
}

// storeFutureMtimeMarker records that target was just repaired, so a later
// process (this gate's OWN next run) can tell "first offense" from
// "recurring — the clock is wrong" apart. Best-effort: a write failure here
// only costs the next run repairing once more than strictly necessary,
// never a wrong verdict about the code under test.
func storeFutureMtimeMarker(target string, wipedAt time.Time) {
	path := futureMtimeMarkerPath(target)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(futureMtimeMarker{WipedAt: wipedAt})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// scanForFutureMtime walks target for the first file whose mtime is after
// cutoff, "" when none is found. One hit is enough — once a shared target
// dir carries a single future-stamped artifact the whole dir is
// untrustworthy (cargo's freshness check can true a stale package against
// another package's fresh-looking dependency graph), so there is nothing to
// gain from finding every offender before deciding.
func scanForFutureMtime(target string, cutoff time.Time) (path string, mtime time.Time) {
	_ = filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if path != "" {
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
			path, mtime = p, info.ModTime()
			return filepath.SkipAll
		}
		return nil
	})
	return path, mtime
}

// invalidateFutureStampedArtifacts wipes target the FIRST time it finds a
// future-stamped artifact. A SECOND hit recorded within futureMtimeMarkerExpiry
// of that repair is not a new incident — it is the same clock producing the
// same symptom right after a rebuild — and the honest response is to say so
// and stop, never to keep paying the wipe's cost every run. Called with the
// caller's (runCargoLocked's) target lock already held, so nothing else is
// writing into target while this runs.
func invalidateFutureStampedArtifacts(target string) {
	if target == "" {
		return
	}
	if _, seen := futureMtimeChecked.LoadOrStore(target, true); seen {
		return
	}
	now := time.Now()
	futurePath, futureMTime := scanForFutureMtime(target, now.Add(futureMtimeTolerance))
	if futurePath == "" {
		return
	}
	ahead := futureMTime.Sub(now)
	if prior, ok := loadFutureMtimeMarker(target); ok && now.Sub(prior.WipedAt) < futureMtimeMarkerExpiry {
		fmt.Fprintf(os.Stderr,
			"gate: %s is future-stamped again (%s, mtime %s, %s ahead of now) after a repair already ran at %s — the clock looks persistently wrong, not a one-off; not wiping again (fix the system clock)\n",
			target, futurePath, futureMTime.Format(time.RFC3339), ahead, prior.WipedAt.Format(time.RFC3339))
		AppendGateLog("buildlock", target, "future-mtime", "repeat-no-wipe", 0)
		return
	}
	suspect := ""
	if ahead > futureMtimeLargeSkew {
		suspect = " (well past ordinary clock jitter — repairing once, but a repeat within a week will not be)"
	}
	fmt.Fprintf(os.Stderr,
		"gate: %s carries a future-stamped artifact (%s, mtime %s, %s ahead of now)%s — the whole target dir is untrustworthy, wiping it before the build\n",
		target, futurePath, futureMTime.Format(time.RFC3339), ahead, suspect)
	AppendGateLog("buildlock", target, "future-mtime", "target-wiped", 0)
	os.RemoveAll(target)
	storeFutureMtimeMarker(target, now)
}
