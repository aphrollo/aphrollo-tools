package release

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

// A changelog section opens at a level-2 heading naming the version, in any of
// the spellings a changelog uses: `## 1.0.0`, `## v1.0.0`, `## [1.0.0] - date`.
// Any level-1 or level-2 heading closes the section before it; a level-3
// heading inside it is the section's own structure.
var (
	sectionBreak   = regexp.MustCompile(`^#{1,2}[ \t]`)
	versionHeading = regexp.MustCompile(`^##[ \t]+\[?v?([0-9]+\.[0-9]+\.[0-9]+)\]?(?:[ \t].*)?$`)
	firstSection   = regexp.MustCompile(`(?m)^##[ \t]+\[?v?[0-9]+\.[0-9]+\.[0-9]+\]?(?:[ \t].*)?$`)
)

// Section is one version section of CHANGELOG.md.
type Section struct {
	// Version is the bare MAJOR.MINOR.PATCH the heading names.
	Version string
	// Heading is the heading line as written.
	Heading string
	// Text is the words under the heading, trimmed, with LF line ends.
	Text string
}

// Sections reads the version sections of a changelog, in file order.
func Sections(text string) []Section {
	var out []Section
	var current *Section
	var words []string
	flush := func() {
		if current != nil {
			current.Text = strings.TrimSpace(strings.Join(words, "\n"))
			out = append(out, *current)
		}
		current, words = nil, nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if sectionBreak.MatchString(line) {
			flush()
			if m := versionHeading.FindStringSubmatch(line); m != nil {
				current = &Section{Version: m[1], Heading: line}
			}
			continue
		}
		if current != nil {
			words = append(words, line)
		}
	}
	flush()
	return out
}

// SectionBody is the words of the section for v.
func SectionBody(text string, v compat.Version) (string, bool) {
	for _, s := range Sections(text) {
		if s.Version == v.String() {
			return s.Text, true
		}
	}
	return "", false
}

// ChangedSections names the versions whose section differs between base and
// head: edited (words or heading), removed or added, sorted. What stands
// outside the sections, the preamble, may change freely. Released sections are
// history, so a PR that changes one is refused.
func ChangedSections(base, head string) []string {
	key := func(s Section) string { return s.Heading + "\n" + s.Text }
	was := map[string]string{}
	for _, s := range Sections(base) {
		was[s.Version] = key(s)
	}
	is := map[string]string{}
	for _, s := range Sections(head) {
		is[s.Version] = key(s)
	}
	var changed []string
	for v, k := range was {
		if got, kept := is[v]; !kept || got != k {
			changed = append(changed, v)
		}
	}
	for v := range is {
		if _, existed := was[v]; !existed {
			changed = append(changed, v)
		}
	}
	slices.Sort(changed)
	return changed
}

// levelRank orders the levels, biggest first when sorted descending.
func levelRank(l compat.Bump) int {
	switch l {
	case compat.BumpMajor:
		return 3
	case compat.BumpMinor:
		return 2
	case compat.BumpPatch:
		return 1
	}
	return 0
}

// Notes is what a set of fragments says as one release's notes: the words of
// each, the biggest level first and then by name, a blank line between.
func Notes(fragments []Fragment) string {
	ordered := slices.Clone(fragments)
	slices.SortFunc(ordered, func(a, b Fragment) int {
		if d := levelRank(b.Level) - levelRank(a.Level); d != 0 {
			return d
		}
		return strings.Compare(a.Name, b.Name)
	})
	parts := make([]string, len(ordered))
	for i, f := range ordered {
		parts[i] = f.Body
	}
	return strings.Join(parts, "\n\n")
}

// ReleaseNotes is one release after the frozen changelog: its heading text
// (the version and date) and the fragments it first contained.
type ReleaseNotes struct {
	Heading   string
	Fragments []Fragment
}

// Assemble is the full changelog: the frozen file's own words up to its first
// version section, then the unreleased block, then each release newest first
// (releases is given in that order), then the frozen sections. With nothing to
// add it is the frozen text byte for byte.
func Assemble(frozen string, unreleased []Fragment, releases []ReleaseNotes) string {
	var blocks []string
	if len(unreleased) > 0 {
		blocks = append(blocks, fmt.Sprintf("## Unreleased\n\n%s\n\n", Notes(unreleased)))
	}
	for _, r := range releases {
		blocks = append(blocks, fmt.Sprintf("## %s\n\n%s\n\n", r.Heading, Notes(r.Fragments)))
	}
	if len(blocks) == 0 {
		return frozen
	}
	generated := strings.Join(blocks, "")
	at := firstSection.FindStringIndex(frozen)
	if at == nil {
		return strings.TrimRight(frozen, "\n") + "\n\n" + strings.TrimRight(generated, "\n") + "\n"
	}
	return frozen[:at[0]] + generated + frozen[at[0]:]
}
