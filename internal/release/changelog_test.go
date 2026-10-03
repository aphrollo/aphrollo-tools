package release

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

const sampleChangelog = `# Changelog

Preamble that says what the file is.

## 1.1.0 - 2026-11-01

A law now reads comments.

### What you will notice

- One more finding.

## [1.0.0]

The first versioned release.

## v0.9.0

Earlier.
`

func TestSections_ReadsEachVersionSectionWithItsWords(t *testing.T) {
	got := Sections(sampleChangelog)
	var versions []string
	for _, s := range got {
		versions = append(versions, s.Version)
	}
	if want := []string{"1.1.0", "1.0.0", "0.9.0"}; !reflect.DeepEqual(versions, want) {
		t.Fatalf("Sections versions = %v, want %v", versions, want)
	}
	if !strings.Contains(got[0].Text, "- One more finding.") || strings.Contains(got[0].Text, "The first versioned release") {
		t.Errorf("the 1.1.0 section = %q, want its own words, a level-3 heading included, and nothing of the next section", got[0].Text)
	}
}

func TestSections_ReadsAFileWrittenWithWindowsLineEndings(t *testing.T) {
	got := Sections("# Changelog\r\n\r\n## 1.0.0\r\n\r\nText.\r\n")
	if len(got) != 1 || got[0].Version != "1.0.0" || strings.Contains(got[0].Text, "\r") {
		t.Fatalf("Sections = %+v, want one 1.0.0 section with LF line ends", got)
	}
}

func TestSections_AVersionOnlyInProseOrALevelThreeHeadingIsNoSection(t *testing.T) {
	if got := Sections("## Notes\n\nRelease 1.0.0 is out.\n\n### 2.0.0\n\nText.\n"); len(got) != 0 {
		t.Fatalf("Sections = %+v, want none", got)
	}
}

func TestChangedSections_NamesWhatAReleasedSectionLostOrGained(t *testing.T) {
	cases := []struct {
		name string
		head string
		want []string
	}{
		{"nothing changed", sampleChangelog, nil},
		{"the preamble may change", strings.Replace(sampleChangelog, "Preamble that says what the file is.", "A pointer to the fragments.", 1), nil},
		{"a section is edited", strings.Replace(sampleChangelog, "The first versioned release.", "The first release.", 1), []string{"1.0.0"}},
		{"a section is removed", strings.Replace(sampleChangelog, "## v0.9.0\n\nEarlier.\n", "", 1), []string{"0.9.0"}},
		{"a section is added", "## 1.2.0\n\nNew.\n\n" + strings.TrimPrefix(sampleChangelog, "# Changelog\n\n"), []string{"1.2.0"}},
		{"trailing blank lines are not words", strings.Replace(sampleChangelog, "Earlier.\n", "Earlier.\n\n\n", 1), nil},
	}
	for _, c := range cases {
		if got := ChangedSections(sampleChangelog, c.head); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ChangedSections = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNotes_JoinsTheWordsBiggestLevelFirstThenByName(t *testing.T) {
	got := Notes([]Fragment{
		{Name: "b", Level: compat.BumpPatch, Body: "Patch B."},
		{Name: "a", Level: compat.BumpPatch, Body: "Patch A."},
		{Name: "z", Level: compat.BumpMajor, Body: "Major Z."},
		{Name: "m", Level: compat.BumpMinor, Body: "Minor M."},
	})
	if want := "Major Z.\n\nMinor M.\n\nPatch A.\n\nPatch B."; got != want {
		t.Fatalf("Notes = %q, want %q", got, want)
	}
}

func TestAssemble_PutsUnreleasedThenNewestReleaseFirstAheadOfTheFrozenSections(t *testing.T) {
	frozen := "# Changelog\n\nPointer line.\n\n## 1.6.6 - 2026-10-03\n\nFrozen words.\n\n## 1.6.5\n\nOlder words.\n"
	got := Assemble(frozen,
		[]Fragment{{Name: "u", Level: compat.BumpPatch, Body: "Not yet released."}},
		[]ReleaseNotes{
			{Heading: "1.7.1 - 2026-10-05", Fragments: []Fragment{{Name: "p", Level: compat.BumpPatch, Body: "Patch words."}}},
			{Heading: "1.7.0 - 2026-10-04", Fragments: []Fragment{{Name: "f", Level: compat.BumpMinor, Body: "Feature words."}}},
		})
	want := "# Changelog\n\nPointer line.\n\n" +
		"## Unreleased\n\nNot yet released.\n\n" +
		"## 1.7.1 - 2026-10-05\n\nPatch words.\n\n" +
		"## 1.7.0 - 2026-10-04\n\nFeature words.\n\n" +
		"## 1.6.6 - 2026-10-03\n\nFrozen words.\n\n## 1.6.5\n\nOlder words.\n"
	if got != want {
		t.Fatalf("Assemble =\n%s\nwant\n%s", got, want)
	}
}

func TestAssemble_WithNothingNewIsTheFrozenFileUntouched(t *testing.T) {
	if got := Assemble(sampleChangelog, nil, nil); got != sampleChangelog {
		t.Fatalf("Assemble = %q, want the frozen text byte for byte", got)
	}
}

func TestAssemble_AFrozenFileWithNoSectionsGetsTheNewOnesAtTheEnd(t *testing.T) {
	got := Assemble("# Changelog\n\nPointer.\n", nil, []ReleaseNotes{{Heading: "1.7.0", Fragments: []Fragment{{Name: "f", Level: compat.BumpMinor, Body: "Words."}}}})
	if want := "# Changelog\n\nPointer.\n\n## 1.7.0\n\nWords.\n"; got != want {
		t.Fatalf("Assemble = %q, want %q", got, want)
	}
}

func TestSectionBody_IsTheWordsOfOneFrozenVersion(t *testing.T) {
	if got, ok := SectionBody(sampleChangelog, compat.Version{Major: 1}); !ok || got != "The first versioned release." {
		t.Fatalf("SectionBody(1.0.0) = %q, %v", got, ok)
	}
	if _, ok := SectionBody(sampleChangelog, compat.Version{Major: 3}); ok {
		t.Fatal("SectionBody found a section for a version the changelog lacks")
	}
}

// CHANGELOG.md is the record of the hand-written releases and stays so: its
// opening says where later releases live, and a release after it that landed here as a
// section would be a second place a release is written.
func TestChangelogFile_IsTheFrozenRecordThatPointsAtTheFragments(t *testing.T) {
	// tree-read-ok: the changelog is the repo's own file, and the check is that it is the frozen record.
	data, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("CHANGELOG.md: %v", err)
	}
	text := string(data)
	sections := Sections(text)
	frozen, found := FrozenThrough(text)
	if len(sections) == 0 || !found || sections[0].Version != frozen.String() {
		t.Fatalf("CHANGELOG.md has sections %v and its newest is not first: keep it newest first", sections)
	}
	opening := text[:strings.Index(text, sections[0].Heading)]
	if !strings.Contains(opening, "changelog.d") || !strings.Contains(opening, "Releases") {
		t.Fatalf("CHANGELOG.md's opening does not say that later releases live in changelog.d and in GitHub Releases:\n%s", opening)
	}
}

// The frozen record ends at whichever release was last written by hand, which
// is the newest section the file carries, wherever it sits and however the
// sections are spelled: no number is written into the code for a merge to race.
func TestFrozenThrough_IsTheNewestSectionTheChangelogCarries(t *testing.T) {
	got, ok := FrozenThrough("# Changelog\n\n## 1.6.5\n\nOld.\n\n## v1.10.0 - 2026-10-04\n\nNewest.\n\n## [1.9.9]\n\nMiddle.\n")
	if !ok || got != (compat.Version{Major: 1, Minor: 10}) {
		t.Fatalf("FrozenThrough = %v, %v; want 1.10.0", got, ok)
	}
	if got, ok := FrozenThrough("# Changelog\n\nNo sections.\n"); ok || got != (compat.Version{}) {
		t.Fatalf("FrozenThrough of a changelog with no sections = %v, %v; want none", got, ok)
	}
}
