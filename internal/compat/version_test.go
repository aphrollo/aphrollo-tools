package compat

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

func TestParseVersion_ReadsMajorMinorPatch(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"1.4.2", Version{1, 4, 2}},
		{"0.0.0", Version{0, 0, 0}},
		{"10.20.30", Version{10, 20, 30}},
	}
	for _, c := range cases {
		got, err := ParseVersion(c.in)
		if err != nil {
			t.Errorf("ParseVersion(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseVersion_RefusesAnythingElse(t *testing.T) {
	for _, in := range []string{"", "1", "1.4", "1.4.2.1", "v1.4.2", "1.4.x", "1.-4.2", " 1.4.2", "1.4.2\n", "1.04.2", "01.4.2", "1.4.2-rc1", "99999999999999999999.0.0"} {
		if got, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %+v, want an error", in, got)
		}
	}
}

func TestVersion_StringIsTheThreeNumbersJoinedByDots(t *testing.T) {
	if got, want := (Version{3, 0, 12}).String(), "3.0.12"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestVersion_Less_ComparesNumbersNotText(t *testing.T) {
	cases := []struct {
		a, b Version
		less bool
	}{
		{Version{1, 3, 0}, Version{1, 4, 0}, true},
		{Version{1, 4, 0}, Version{1, 3, 0}, false},
		{Version{1, 4, 0}, Version{1, 4, 0}, false},
		{Version{1, 9, 0}, Version{1, 10, 0}, true},
		{Version{1, 10, 0}, Version{1, 9, 0}, false},
		{Version{1, 99, 99}, Version{2, 0, 0}, true},
		{Version{2, 0, 0}, Version{1, 99, 99}, false},
		{Version{1, 4, 1}, Version{1, 4, 2}, true},
		{Version{1, 4, 2}, Version{1, 4, 1}, false},
	}
	for _, c := range cases {
		if got := c.a.Less(c.b); got != c.less {
			t.Errorf("%v.Less(%v) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}

func TestParseRequires_ReadsTheOldestAcceptedVersion(t *testing.T) {
	cases := []struct {
		in   string
		min  Version
		text string
	}{
		{">=1.4", Version{1, 4, 0}, ">=1.4"},
		{">=1.4.2", Version{1, 4, 2}, ">=1.4.2"},
		{">= 2.0", Version{2, 0, 0}, ">=2.0"},
		{"  >=0.9  ", Version{0, 9, 0}, ">=0.9"},
	}
	for _, c := range cases {
		got, err := ParseRequires(c.in)
		if err != nil {
			t.Errorf("ParseRequires(%q) error: %v", c.in, err)
			continue
		}
		if got.Min != c.min || got.String() != c.text {
			t.Errorf("ParseRequires(%q) = {%v, %q}, want {%v, %q}", c.in, got.Min, got.String(), c.min, c.text)
		}
	}
}

func TestParseRequires_RefusesWhatItCannotCompareAndSaysHowToFixIt(t *testing.T) {
	for _, in := range []string{"", "1.4", ">1.4", "<=1.4", "~1.4", "^1.4", "=1.4", ">=1", ">=1.4.2.1", ">=v1.4", ">=latest", "latest", ">=1.4 <2", ">=1.4,<2", ">=", ">=01.4"} {
		_, err := ParseRequires(in)
		if err == nil {
			t.Errorf("ParseRequires(%q) succeeded, want an error", in)
			continue
		}
		if !strings.Contains(err.Error(), `requires = ">=1.4"`) {
			t.Errorf("ParseRequires(%q) error = %q, want it to show the working form requires = \">=1.4\"", in, err)
		}
	}
}

// The binary's own version is the one in the VERSION file, parsed once; a
// version file that does not parse would make every compare answer for a
// binary that does not exist, so it fails every test and every start instead.
func TestBinary_IsTheVersionTheSourceCarries(t *testing.T) {
	if got, want := Binary().String(), buildinfo.Version(); got != want {
		t.Fatalf("Binary() = %q, want %q", got, want)
	}
}
