package postedit

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// EffectiveTDD is the `tdd` mode the lane dir stands in runs under, and why: a value
// any layer pins, else the arm the lane's repo and name hash to (tddarm.Resolve). It
// reads the config layers and the checkout's own files and spawns nothing, so a hook
// may ask it on every call.
func EffectiveTDD(dir string) tddarm.Mode {
	repo := compat.RepoRoot(dir)
	return tddarm.Resolve(config.ForDir(dir).Get("tdd"), tddarm.RepoKey(repo), LaneOf(repo))
}

// RecordLaneArm writes the event that says which arm a lane is in, the first time the
// lane is seen and never again: one marker file per (repo, lane) under the state
// directory is created exclusively, and only the call that created it writes. A lane
// in no arm (a trunk branch, no branch) records nothing; a pinned lane records that
// it is pinned, so the lanes the experiment leaves out are counted too.
func RecordLaneArm(root, session string, m tddarm.Mode) {
	lane := LaneOf(root)
	if lane == "" || m.Why == tddarm.WhyNoLane {
		return
	}
	dir := StateDir()
	if dir == "" {
		return
	}
	sum := sha256.Sum256([]byte(tddarm.RepoKey(root) + "\x00" + lane))
	marker := filepath.Join(dir, "lane-arm", hex.EncodeToString(sum[:8]))
	if os.MkdirAll(filepath.Dir(marker), 0o700) != nil {
		return
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return // already recorded, or a state directory that cannot be written
	}
	_ = f.Close()
	detail := map[string]string{"mode": m.TDD, "why": m.Why}
	if m.Arm != "" {
		detail["arm"] = m.Arm
	}
	if m.Layer != "" {
		detail["layer"] = m.Layer
	}
	AppendEvent(Event{Kind: "lane-arm", Root: root, Actor: session, Detail: detail})
}
