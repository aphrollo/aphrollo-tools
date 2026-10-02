package compat

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleChangelog = `# Changelog

## 1.1.0 - 2026-11-01

A law now reads comments.

## 1.0.0

### What you will notice

The first versioned release.

## 0.9.0

Earlier.
`

func TestChangelogHas_FindsAVersionsSection(t *testing.T) {
	for _, v := range []Version{{1, 1, 0}, {1, 0, 0}, {0, 9, 0}} {
		if !ChangelogHas(sampleChangelog, v) {
			t.Errorf("ChangelogHas(%v) = false, want true", v)
		}
	}
}

func TestChangelogHas_AVersionWithNoSectionIsAbsent(t *testing.T) {
	for _, v := range []Version{{1, 2, 0}, {1, 0, 1}, {2, 0, 0}, {1, 10, 0}} {
		if ChangelogHas(sampleChangelog, v) {
			t.Errorf("ChangelogHas(%v) = true, want false", v)
		}
	}
}

func TestChangelogHas_ANumberThatOnlyStartsLikeTheVersionIsADifferentVersion(t *testing.T) {
	text := "## 1.10.0\n\nTen.\n\n## 11.0.0\n\nEleven.\n"
	if ChangelogHas(text, Version{1, 1, 0}) {
		t.Error("ChangelogHas(1.1.0) = true for a changelog with only 1.10.0 and 11.0.0")
	}
	if ChangelogHas(text, Version{1, 0, 0}) {
		t.Error("ChangelogHas(1.0.0) = true for a changelog with only 1.10.0 and 11.0.0")
	}
}

func TestChangelogHas_AHeadingWithNothingUnderItIsNotASection(t *testing.T) {
	if ChangelogHas("## 1.0.0\n\n## 0.9.0\n\nEarlier.\n", Version{1, 0, 0}) {
		t.Error("an empty section followed by another counted as a changelog section")
	}
	if ChangelogHas("## 0.9.0\n\nEarlier.\n\n## 1.0.0\n\n", Version{1, 0, 0}) {
		t.Error("an empty section at the end counted as a changelog section")
	}
}

func TestChangelogHas_OnlyALevelTwoHeadingOpensASection(t *testing.T) {
	if ChangelogHas("## Earlier\n\n### 1.0.0\n\nText.\n", Version{1, 0, 0}) {
		t.Error("a level-3 heading counted as a version section")
	}
	if ChangelogHas("Release 1.0.0 is out.\n\n## Notes\n\n1.0.0 shipped.\n", Version{1, 0, 0}) {
		t.Error("prose mentioning the version counted as a section")
	}
}

func TestChangelogHas_AcceptsTheCommonHeadingSpellings(t *testing.T) {
	for _, heading := range []string{"## 1.0.0", "## [1.0.0] - 2026-10-02", "## v1.0.0", "##\t1.0.0 (2026-10-02)"} {
		if !ChangelogHas(heading+"\n\nText.\n", Version{1, 0, 0}) {
			t.Errorf("heading %q was not read as the 1.0.0 section", heading)
		}
	}
}

func TestChangelogHas_ReadsAFileWrittenWithWindowsLineEndings(t *testing.T) {
	if !ChangelogHas("# Changelog\r\n\r\n## 1.0.0\r\n\r\nText.\r\n", Version{1, 0, 0}) {
		t.Error("a CRLF changelog's 1.0.0 section was not found")
	}
}

func TestChangelogHas_ASectionAtTheEndOfTheFileWithNoFinalNewline(t *testing.T) {
	if !ChangelogHas("## 1.0.0\n\nText.", Version{1, 0, 0}) {
		t.Error("a last section with no trailing newline was not found")
	}
}

// The version in the source is the version the changelog must explain: a bump
// that left CHANGELOG.md as it was would ship a release the consumers it
// affects were never told about.
func TestChangelog_TheSourceVersionHasASection(t *testing.T) {
	// tree-read-ok: the changelog is the repo's own file, and the check is that it names the version in VERSION.
	data, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("CHANGELOG.md: %v", err)
	}
	if !ChangelogHas(string(data), Binary()) {
		t.Fatalf("CHANGELOG.md has no section for version %s: add a `## %s` heading with what a consumer will notice and what migrates by itself", Binary(), Binary())
	}
}
