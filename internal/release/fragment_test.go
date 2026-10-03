package release

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

func TestParseFragment_ReadsTheLevelAndTheWordsUnderIt(t *testing.T) {
	got, err := ParseFragment("lane-x", "level: Minor\n\nA law now reads comments.\n\n- It flags one more thing.\n")
	if err != nil {
		t.Fatalf("ParseFragment error: %v", err)
	}
	want := Fragment{Name: "lane-x", Level: compat.BumpMinor, Body: "A law now reads comments.\n\n- It flags one more thing."}
	if got != want {
		t.Fatalf("ParseFragment = %+v, want %+v", got, want)
	}
}

func TestParseFragment_ReadsAFileWrittenWithWindowsLineEndings(t *testing.T) {
	got, err := ParseFragment("x", "\r\nlevel: patch\r\n\r\nFixes a typo.\r\nSecond line.\r\n")
	if err != nil || got.Level != compat.BumpPatch || got.Body != "Fixes a typo.\nSecond line." {
		t.Fatalf("ParseFragment = %+v, %v; want a patch fragment with LF-joined body", got, err)
	}
}

func TestParseFragment_RefusesWhatIsNotAFragmentAndSaysHow(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"empty file", "", "starts with a `level:` line"},
		{"words but no level", "A law now reads comments.\n", "starts with a `level:` line"},
		{"level after the words", "Words.\n\nlevel: minor\n", "starts with a `level:` line"},
		{"unknown level", "level: huge\n\nWords.\n", "level: huge"},
		{"none is not a fragment level", "level: none\n\nWords.\n", "a change of level none adds no fragment"},
		{"level but no words", "level: minor\n\n   \n", "no words under its level line"},
		{"a heading would open a false section", "level: minor\n\nWords.\n\n## 9.9.9\n\nMore.\n", "changelog heading"},
		{"a title heading too", "level: minor\n\n# Title\n\nWords.\n", "changelog heading"},
	}
	for _, c := range cases {
		_, err := ParseFragment("x", c.text)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: ParseFragment error = %v, want it to contain %q", c.name, err, c.want)
			continue
		}
		if !strings.Contains(err.Error(), "changelog.d/x.md") {
			t.Errorf("%s: error %q does not name the file changelog.d/x.md", c.name, err)
		}
	}
}

func TestParseFragment_ALevelThreeHeadingIsTheFragmentsOwnStructure(t *testing.T) {
	if _, err := ParseFragment("x", "level: minor\n\nWords.\n\n### What migrates by itself\n\nThe baseline.\n"); err != nil {
		t.Fatalf("a level-3 heading was refused: %v", err)
	}
}

func TestFragmentName_OnlyASlugMdDirectlyInTheFragmentDirIsAFragment(t *testing.T) {
	cases := []struct {
		path string
		name string
		ok   bool
	}{
		{"changelog.d/version-from-tags.md", "version-from-tags", true},
		{"changelog.d/f17_why.v2.md", "f17_why.v2", true},
		{"changelog.d/README.md", "", false},
		{"changelog.d/sub/x.md", "", false},
		{"changelog.d/x.txt", "", false},
		{"changelog.d/.hidden.md", "", false},
		{"changelog.d/has space.md", "", false},
		{"docs/x.md", "", false},
		{"changelog.d/", "", false},
	}
	for _, c := range cases {
		name, ok := FragmentName(c.path)
		if name != c.name || ok != c.ok {
			t.Errorf("FragmentName(%q) = (%q, %v), want (%q, %v)", c.path, name, ok, c.name, c.ok)
		}
	}
}

func TestFragmentPath_IsTheInverseOfFragmentName(t *testing.T) {
	if got, want := FragmentPath("lane-x"), "changelog.d/lane-x.md"; got != want {
		t.Fatalf("FragmentPath = %q, want %q", got, want)
	}
}
