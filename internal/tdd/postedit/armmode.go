package postedit

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// EffectiveTDD is the `tdd` mode the lane dir stands in runs under, and why: a value
// any layer pins, else the arm the lane's repo and name hash to (tddarm.Resolve). It
// reads the config layers and the checkout's own files and spawns nothing, so a hook
// may ask it on every call.
//
// Whether a lane is in the off arm is decided once. A lane whose arm is recorded
// (RecordLaneArm) keeps it: off stays off and on stays on, whatever the share has become.
// A lane not yet recorded takes the share its repo's primary checkout declares, never the
// one its own worktree declares, so a rebase that brings a share in, or a lane that edits
// its own config, cannot move the lane.
func EffectiveTDD(dir string) tddarm.Mode {
	repo := compat.RepoRoot(dir)
	lane := LaneOf(repo)
	cfg := config.ForDir(dir)
	share := 0
	if arm, recorded := recordedArm(repo, lane); recorded {
		if arm == tddarm.ArmOff {
			return tddarm.Mode{TDD: "off", Arm: tddarm.ArmOff, Why: tddarm.WhyAssigned}
		}
	} else {
		share = primaryShare(repo)
	}
	return tddarm.ResolveInShare(cfg.Get("tdd"), repo, lane, share)
}

// primaryShare is the off share the repo's primary checkout declares.
func primaryShare(repo string) int {
	common := config.CommonDir(repo)
	if common == "" {
		return 0
	}
	return config.ForDir(filepath.Dir(common)).Get("ab-off-share").Value.N
}

// laneMarker is the file that records a lane's arm: one per (repo, lane) under the state
// directory. "" when there is no state directory or no lane.
func laneMarker(root, lane string) string {
	dir := StateDir()
	if dir == "" || lane == "" {
		return ""
	}
	root = compat.RepoRoot(root) // one lane is one marker, whichever directory of its checkout asks
	sum := sha256.Sum256([]byte(tddarm.RepoKey(root) + "\x00" + lane))
	return filepath.Join(dir, "lane-arm", hex.EncodeToString(sum[:8]))
}

// recordedArm is the arm a lane's marker holds. A marker an older binary wrote is empty
// and records an on lane: the off arm did not exist then.
func recordedArm(root, lane string) (arm string, recorded bool) {
	marker := laneMarker(root, lane)
	if marker == "" {
		return arm, recorded
	}
	if data, err := os.ReadFile(marker); err == nil {
		arm, recorded = strings.TrimSpace(string(data)), true
	}
	return arm, recorded
}

// RecordLaneArm writes the event that says which arm a lane is in, the first time the
// lane is seen and never again: one marker file per (repo, lane) under the state
// directory is created exclusively, and only the call that created it writes. A lane
// in no arm (a trunk branch, no branch) records nothing; a pinned lane records that
// it is pinned, so the lanes the experiment leaves out are counted too. The marker holds
// the arm, and it is what keeps the lane in it (EffectiveTDD).
func RecordLaneArm(root, session string, m tddarm.Mode) {
	lane := LaneOf(root)
	if lane == "" || m.Why == tddarm.WhyNoLane {
		return
	}
	marker := laneMarker(root, lane)
	if marker == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(marker), 0o700) != nil {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return // already recorded
	}
	detail := map[string]string{"mode": m.TDD, "why": m.Why, "key": filepath.Base(marker)}
	if m.Arm != "" {
		detail["arm"] = m.Arm
	}
	if m.Layer != "" {
		detail["layer"] = m.Layer
	}
	// The event first, the marker once the log holds it: a write that failed is tried again at
	// the next call, and never leaves a lane out of the counts for good. Once-by-key keeps two
	// hooks that race from writing it twice.
	AppendEventOnce(Event{Kind: "lane-arm", Root: root, Actor: session, Detail: detail}, "key")
	logged, ok := loggedLaneArm(root, detail["key"])
	if !ok {
		return
	}
	// The marker holds the arm the log holds, which may be another call's, not this one's.
	if f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.WriteString(logged)
		_ = f.Close()
	}
}

// loggedLaneArm is the arm the repo's event log holds for the lane-arm event of key, "pinned"
// for a lane no arm covers; ok is false when the log holds no such event.
func loggedLaneArm(root, key string) (arm string, ok bool) {
	for _, e := range ReadEvents(root) {
		if e.Kind == "lane-arm" && e.Detail["key"] == key {
			if arm = e.Detail["arm"]; arm == "" {
				arm = "pinned"
			}
			return arm, true
		}
	}
	return "", false
}
