package suite

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// diffHeaderPath reads the post-image path out of a `diff --git a/x b/y` line.
func diffHeaderPath(line string) (string, bool) {
	const prefix = "diff --git "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	i := strings.LastIndex(rest, " b/")
	if i < 0 {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(rest[i+3:]), `"`), true
}

// fixtureLawFromPath extracts the law name from a path under
// .ratchet/fixtures/<law>/..., ok=false when rel names no fixture at all.
func fixtureLawFromPath(rel string) (string, bool) {
	prefix := ratchet.FixturesDir + "/"
	if !strings.HasPrefix(rel, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(rel, prefix)
	i := strings.Index(rest, "/")
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
}
