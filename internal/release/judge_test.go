package release

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

const (
	changelogBase = "# Changelog\n\nPointer.\n\n## 1.6.6\n\nFrozen words.\n"
	fragMinor     = "level: minor\n\nA law now reads comments.\n"
)

func added(files ...string) []FileChange {
	var out []FileChange
	for _, f := range files {
		out = append(out, FileChange{Status: 'A', File: f})
	}
	return out
}

// The rules table of the version check: what a PR may and may not do to the
// version, the changelog and the fragments, one row per rule.
func TestJudgeChange_RulesTable(t *testing.T) {
	cases := []struct {
		name   string
		change Change
		want   []string // a substring per problem, in order; nil means the PR is accepted
	}{
		{
			name:   "none with no fragment is accepted",
			change: Change{Body: "version: none\n", Files: added("internal/cli/x.go")},
		},
		{
			name: "a minor PR with its one minor fragment is accepted",
			change: Change{
				Body:      "version: minor\n",
				Files:     added("internal/cli/x.go", "changelog.d/lane-x.md"),
				Fragments: map[string]string{"changelog.d/lane-x.md": fragMinor},
			},
		},
		{
			name:   "no version line",
			change: Change{Body: "Fixes a typo.\n", Files: added("README.md")},
			want:   []string{"no `version:` line"},
		},
		{
			name:   "a non-none PR with no fragment",
			change: Change{Body: "version: minor\n", Files: added("internal/cli/x.go")},
			want:   []string{"adds no changelog fragment: add changelog.d/<lane>.md starting `level: minor`"},
		},
		{
			name: "a none PR that adds a fragment",
			change: Change{
				Body:      "version: none\n",
				Files:     added("changelog.d/lane-x.md"),
				Fragments: map[string]string{"changelog.d/lane-x.md": fragMinor},
			},
			want: []string{"the body says `version: none` but the PR adds changelog.d/lane-x.md"},
		},
		{
			name: "a fragment whose level contradicts the body",
			change: Change{
				Body:      "version: patch\n",
				Files:     added("changelog.d/lane-x.md"),
				Fragments: map[string]string{"changelog.d/lane-x.md": fragMinor},
			},
			want: []string{"changelog.d/lane-x.md says `level: minor` but the body says `version: patch`"},
		},
		{
			name: "two fragments are one too many",
			change: Change{
				Body:  "version: minor\n",
				Files: added("changelog.d/a.md", "changelog.d/b.md"),
				Fragments: map[string]string{
					"changelog.d/a.md": fragMinor,
					"changelog.d/b.md": fragMinor,
				},
			},
			want: []string{"adds 2 changelog fragments (changelog.d/a.md, changelog.d/b.md): one PR adds exactly one"},
		},
		{
			name: "a fragment that cannot be read",
			change: Change{
				Body:      "version: minor\n",
				Files:     added("changelog.d/lane-x.md"),
				Fragments: map[string]string{"changelog.d/lane-x.md": "Words but no level.\n"},
			},
			want: []string{"starts with a `level:` line"},
		},
		{
			name: "an added file in the fragment directory that is no slug.md",
			change: Change{
				Body:  "version: minor\n",
				Files: added("changelog.d/sub/x.md", "changelog.d/ok.md"),
				Fragments: map[string]string{
					"changelog.d/ok.md": fragMinor,
				},
			},
			want: []string{"changelog.d/sub/x.md is not a fragment: a fragment is changelog.d/<slug>.md"},
		},
		{
			name:   "an edit to VERSION",
			change: Change{Body: "version: none\n", Files: []FileChange{{Status: 'M', File: compat.VersionFile}}},
			want:   []string{compat.VersionFile + " is not edited by a PR"},
		},
		{
			name:   "a re-added VERSION",
			change: Change{Body: "version: none\n", Files: added(compat.VersionFile)},
			want:   []string{compat.VersionFile + " is not edited by a PR"},
		},
		{
			name:   "deleting a VERSION that is still there is the way out",
			change: Change{Body: "version: none\n", Files: []FileChange{{Status: 'D', File: compat.VersionFile}}},
		},
		{
			name: "a released changelog section edited",
			change: Change{
				Body:          "version: none\n",
				Files:         []FileChange{{Status: 'M', File: "CHANGELOG.md"}},
				BaseChangelog: changelogBase,
				HeadChangelog: strings.Replace(changelogBase, "Frozen words.", "Other words.", 1),
			},
			want: []string{"CHANGELOG.md's section for 1.6.6 changed"},
		},
		{
			name: "a new changelog section",
			change: Change{
				Body:          "version: none\n",
				Files:         []FileChange{{Status: 'M', File: "CHANGELOG.md"}},
				BaseChangelog: changelogBase,
				HeadChangelog: "## 1.7.0\n\nNew.\n\n" + changelogBase,
			},
			want: []string{"CHANGELOG.md's section for 1.7.0 changed"},
		},
		{
			name: "the changelog's own preamble may change",
			change: Change{
				Body:          "version: none\n",
				Files:         []FileChange{{Status: 'M', File: "CHANGELOG.md"}},
				BaseChangelog: changelogBase,
				HeadChangelog: strings.Replace(changelogBase, "Pointer.", "Later releases live in changelog.d.", 1),
			},
		},
		{
			name:   "a released fragment edited",
			change: Change{Body: "version: none\n", Files: []FileChange{{Status: 'M', File: "changelog.d/old.md"}}},
			want:   []string{"changelog.d/old.md is a released fragment"},
		},
		{
			name:   "a released fragment deleted",
			change: Change{Body: "version: none\n", Files: []FileChange{{Status: 'D', File: "changelog.d/old.md"}}},
			want:   []string{"changelog.d/old.md is a released fragment"},
		},
		{
			name:   "the fragment directory's README may change",
			change: Change{Body: "version: none\n", Files: []FileChange{{Status: 'M', File: "changelog.d/README.md"}}},
		},
		{
			name:   "a language row with version none",
			change: Change{Body: "version: none\n", Files: added("internal/lang/languages/go.toml")},
			want:   []string{"internal/lang/languages/go.toml changes what a consumer's gate says"},
		},
		{
			name: "a language row with a patch fragment",
			change: Change{
				Body:      "version: patch\n",
				Files:     added("internal/lang/languages/go.toml", "changelog.d/x.md"),
				Fragments: map[string]string{"changelog.d/x.md": "level: patch\n\nWords.\n"},
			},
			want: []string{"internal/lang/languages/go.toml changes what a consumer's gate says"},
		},
		{
			name: "a law preset change at minor",
			change: Change{
				Body:      "version: minor\n",
				Files:     added("internal/ratchet/presets/go/new_law.toml", "changelog.d/x.md"),
				Fragments: map[string]string{"changelog.d/x.md": fragMinor},
			},
		},
		{
			name:   "every problem is named, not just the first",
			change: Change{Body: "nothing\n", Files: []FileChange{{Status: 'M', File: compat.VersionFile}, {Status: 'M', File: "changelog.d/old.md"}}},
			want:   []string{"no `version:` line", compat.VersionFile + " is not edited", "changelog.d/old.md is a released fragment"},
		},
	}
	for _, c := range cases {
		got := JudgeChange(c.change)
		if len(got) != len(c.want) {
			t.Errorf("%s: JudgeChange = %q, want %d problem(s) containing %q", c.name, got, len(c.want), c.want)
			continue
		}
		for i, w := range c.want {
			if !strings.Contains(got[i], w) {
				t.Errorf("%s: problem %d = %q, want it to contain %q", c.name, i, got[i], w)
			}
		}
	}
}

func TestFragmentSummary_NamesTheBodyLevelAndTheFragment(t *testing.T) {
	if got, want := Summary("minor", []string{"changelog.d/lane-x.md"}), "minor, changelog.d/lane-x.md"; got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}
	if got, want := Summary("none", nil), "none"; got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}
}

// ratchet: test_removed internal/compat/changelog_test.go: the changelog is no longer checked for a section per VERSION; released sections are frozen (TestChangedSections_NamesWhatAReleasedSectionLostOrGained) and a release's notes are its fragments
