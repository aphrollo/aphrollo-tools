// Package tddarm is the A/B of the `tdd` key (docs/trellis-architecture.md, lanes A1
// to A3 and the open question of the `tdd` default): a lane of a repo that pins no `tdd` value is put in one of two arms, enforce
// or warn, by a pure function of the repo's identity and the lane's name, so every
// session and every box puts a lane in the same arm. A repo or a user that pins `tdd`
// is outside the experiment: the pin wins and no arm is recorded.
//
// The package reads nothing but the config setting it is handed and, for the repo's
// identity, two small files (RepoKey); it spawns nothing.
package tddarm

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/config"
)

// The arms: the `tdd` value a lane runs under.
const (
	ArmEnforce = "enforce"
	ArmWarn    = "warn"
	// ArmOff is the control of the on/off comparison: aphrollo runs no hook and no gate in
	// the lane. It is assigned only when the repo declares an ab-off-share above 0.
	ArmOff = "off"
)

// Why a lane runs under the mode it does.
const (
	WhyPinned   = "pinned"   // a layer above the built-in declares `tdd`
	WhyAssigned = "assigned" // the lane's arm is the hash of its repo and name
	WhyNoLane   = "no-lane"  // no branch to assign, or a trunk branch: the built-in value
)

// Mode is the `tdd` mode a lane runs under, and why.
type Mode struct {
	TDD   string // enforce, warn or off
	Arm   string // ArmEnforce or ArmWarn for an assigned lane, "" otherwise
	Why   string // WhyPinned, WhyAssigned or WhyNoLane
	Layer string // for a pin, the layer that declared it
}

// Of is the arm a lane is assigned to. The hash is FNV-1a over a fixed salt, the repo
// key and the lane's name, read at bit 16: the lowest bit of FNV-1a is the parity of
// the odd bytes of the input, which would put lanes named in a run into alternating
// arms.
func Of(repoKey, lane string) string {
	h := uint32(2166136261)
	for _, b := range []byte("tdd-arm\x00" + repoKey + "\x00" + lane) {
		h = (h ^ uint32(b)) * 16777619
	}
	if (h>>16)&1 == 1 {
		return ArmWarn
	}
	return ArmEnforce
}

// Off reports whether the lane runs with aphrollo off: in the off arm of the on/off
// comparison. A lane pinned to tdd = off is not: there only the red-green rule is off.
func (m Mode) Off() bool { return m.Arm == ArmOff }

// OffBucket is the lane's place, 0 to 99, in the off assignment: FNV-1a over a salt of
// its own, the repo key and the lane's name, finished with murmur3's mixer so every
// bit of the hash counts. The salt differs from Of's, so a lane's bucket says nothing of
// its enforce or warn arm. A lane is off when its bucket is below the repo's share.
func OffBucket(repoKey, lane string) int {
	h := uint32(2166136261)
	for _, b := range []byte("off-arm" + sep + repoKey + sep + lane) {
		h = (h ^ uint32(b)) * 16777619
	}
	return finish(h)
}

const sep = "\x00"

func finish(h uint32) int {
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	h *= 0xc2b2ae35
	h ^= h >> 16
	return int(h % 100)
}

// ResolveShare is Resolve for a repo that declares an off share, in percent. A pin still
// wins, and a lane that has none (a trunk branch, no branch) is in no arm; otherwise a
// lane whose OffBucket is below the share runs with aphrollo off, and the rest are
// assigned as Resolve assigns them. A share of 0 is Resolve itself.
func ResolveShare(set config.Setting, repoKey, lane string, share int) Mode {
	m := Resolve(set, repoKey, lane)
	if m.Why == WhyAssigned && OffBucket(repoKey, lane) < share {
		return Mode{TDD: "off", Arm: ArmOff, Why: WhyAssigned}
	}
	return m
}

// Resolve is the mode a lane runs under. set is the `tdd` setting as the config
// layers resolved it: a value any layer above the built-in declared is a pin, and wins;
// a value a layer declared wrongly reads as the built-in one (config.Setting.Fallback)
// and pins nothing. Otherwise the lane takes its assigned arm; with no lane, or on a
// trunk branch, where no change is made to join an outcome to, the built-in value
// stands and the lane is in no arm.
func Resolve(set config.Setting, repoKey, lane string) Mode {
	if set.Layer != config.BuiltIn {
		return Mode{TDD: set.Value.S, Why: WhyPinned, Layer: set.Layer.String()}
	}
	switch lane {
	case "", "main", "master", "@trunk":
		return Mode{TDD: set.Value.S, Why: WhyNoLane}
	}
	arm := Of(repoKey, lane)
	return Mode{TDD: arm, Arm: arm, Why: WhyAssigned}
}

// RepoKey is the repo's identity for the assignment: the origin remote without its
// scheme, user, port-less host separator and `.git`, lower-cased, so every clone of
// the repo on every box has the one key; for a repo with no origin, the name of its
// main checkout's directory. "" for a root that holds no repo. A linked worktree has
// its main checkout's key.
func RepoKey(root string) string {
	common := config.CommonDir(root)
	if common == "" {
		return ""
	}
	if url := originURL(filepath.Join(common, "config")); url != "" {
		return normalizeRemote(url)
	}
	return strings.ToLower(filepath.Base(filepath.Dir(common)))
}

// originURL is the url of the [remote "origin"] section of a git config file, "".
func originURL(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.EqualFold(strings.Join(strings.Fields(line), " "), `[remote "origin"]`)
			continue
		}
		if key, val, ok := strings.Cut(line, "="); ok && inOrigin && strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// normalizeRemote makes the spellings of one remote one string:
// github.com/org/repo for https://, ssh:// and scp-like urls, with or without
// credentials, a trailing slash or .git.
func normalizeRemote(url string) string {
	u := strings.TrimSpace(url)
	if _, rest, ok := strings.Cut(u, "://"); ok {
		u = rest
	}
	if _, rest, ok := strings.Cut(u, "@"); ok {
		u = rest
	}
	// scp-like: host:org/repo
	if host, path, ok := strings.Cut(u, ":"); ok && !strings.Contains(host, "/") {
		u = host + "/" + path
	}
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, ".git")
	return strings.ToLower(u)
}

// Trunk is the branch origin/HEAD of the repo names ("develop"), read from the file
// refs/remotes/origin/HEAD of the common git directory; "" when that is not a symbolic
// ref there (never set, or not a repo), and the usual names of Resolve then stand. It
// spawns no git.
func Trunk(root string) string {
	common := config.CommonDir(root)
	if common == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(common, "refs", "remotes", "origin", "HEAD"))
	if err != nil {
		return ""
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "ref: refs/remotes/origin/")
	if !ok {
		return ""
	}
	return target
}

// ResolveIn is Resolve for the lane of the checkout at root: its repo key from the
// remote, and its trunk, whatever it is called, in no arm.
func ResolveIn(set config.Setting, root, lane string) Mode {
	return ResolveInShare(set, root, lane, 0)
}

// ResolveInShare is ResolveIn for a repo that declares an off share.
func ResolveInShare(set config.Setting, root, lane string, share int) Mode {
	if lane != "" && lane == Trunk(root) {
		lane = ""
	}
	return ResolveShare(set, RepoKey(root), lane, share)
}
