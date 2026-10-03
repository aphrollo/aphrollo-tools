package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// maxNameLen is where a lane's file name stops carrying the lane itself: the
// state root is on a short path (#1112) and a branch can be as long as git
// lets it.
const maxNameLen = 80

// laneFileName is the file stem a lane's checkpoint and lock live under. A
// lane key is a branch (it holds slashes) or @trunk, and Windows compares
// names without case, reserves device names and drops a trailing dot, so
// everything but a lower-case letter, digit, dash, underscore and an inner dot
// is written as %xx (lower-case hex, so an upper-case letter is never a
// letter): two lanes never share a name on any filesystem. A name past
// maxNameLen keeps its head and a hash of the whole lane.
func laneFileName(lane string) string {
	var b strings.Builder
	for i := range len(lane) {
		c := lane[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.' && i > 0 && i < len(lane)-1:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02x", c)
		}
	}
	name := b.String()
	if isDeviceName(strings.SplitN(name, ".", 2)[0]) {
		name = fmt.Sprintf("%%%02x", name[0]) + name[1:]
	}
	if len(name) > maxNameLen {
		sum := sha256.Sum256([]byte(lane))
		name = name[:64] + "~" + hex.EncodeToString(sum[:8])
	}
	return name
}

// isDeviceName reports whether stem is one of the names Windows reserves for a
// device, which a file of any extension cannot be named.
func isDeviceName(stem string) bool {
	switch stem {
	case "con", "prn", "aux", "nul":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) && stem[3] >= '1' && stem[3] <= '9'
}
