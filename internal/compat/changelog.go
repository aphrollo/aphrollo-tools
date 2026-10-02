package compat

import (
	"regexp"
	"strings"
)

// A changelog section opens at a level-2 heading naming the version, in any of
// the spellings a changelog uses: `## 1.0.0`, `## v1.0.0`, `## [1.0.0] - date`.
// Any level-1 or level-2 heading closes the section before it; a level-3
// heading inside it is the section's own structure.
var (
	sectionBreak   = regexp.MustCompile(`^#{1,2}[ \t]`)
	versionHeading = regexp.MustCompile(`^##[ \t]+\[?v?([0-9]+\.[0-9]+\.[0-9]+)\]?(?:[ \t].*)?$`)
)

// ChangelogHas reports whether text carries a section for v with something
// written under it. An empty heading is what a bump with no words behind it
// looks like, and a consumer reading it learns nothing.
func ChangelogHas(text string, v Version) bool {
	want := v.String()
	inSection, hasBody := false, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if sectionBreak.MatchString(line) {
			if inSection && hasBody {
				return true
			}
			m := versionHeading.FindStringSubmatch(line)
			inSection, hasBody = m != nil && m[1] == want, false
			continue
		}
		if inSection && strings.TrimSpace(line) != "" {
			hasBody = true
		}
	}
	return inSection && hasBody
}
