package tdd

import (
	"strings"
	"testing"
)

func TestMask_BlanksStringsAndComments(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// substrings that must be GONE from the masked output (they lived
		// inside a string or comment) and substrings that must REMAIN (they
		// are real code).
		gone   []string
		remain []string
	}{
		{
			name:   "double-quoted string",
			src:    `foo("sleep 600 here")`,
			gone:   []string{"sleep 600 here"},
			remain: []string{"foo(", `"`, ")"},
		},
		{
			name:   "single-quoted string",
			src:    `x = 'time.Sleep(2)'`,
			gone:   []string{"time.Sleep(2)"},
			remain: []string{"x =", "'"},
		},
		{
			name:   "backtick template",
			src:    "y = `it.only(z)`",
			gone:   []string{"it.only(z)"},
			remain: []string{"y ="},
		},
		{
			name:   "line comment //",
			src:    "doThing() // assert x == x",
			gone:   []string{"assert x == x"},
			remain: []string{"doThing()"},
		},
		{
			name:   "block comment",
			src:    "a() /* fit(slow) */ b()",
			gone:   []string{"fit(slow)"},
			remain: []string{"a()", "b()"},
		},
		{
			name:   "hash comment",
			src:    "do_thing()  # time.sleep(5)",
			gone:   []string{"time.sleep(5)"},
			remain: []string{"do_thing()"},
		},
		{
			// An escaped quote must NOT terminate the string early and leak the
			// smell that follows it as code (the false-block path).
			name:   "escaped quote inside string",
			src:    `log("say \"it.only(\" now")`,
			gone:   []string{"it.only("},
			remain: []string{"log("},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mask(c.src)
			if len(got) != len(c.src) {
				t.Fatalf("mask changed length: got %d, want %d", len(got), len(c.src))
			}
			for _, g := range c.gone {
				if strings.Contains(got, g) {
					t.Errorf("masked output still contains %q:\n%s", g, got)
				}
			}
			for _, r := range c.remain {
				if !strings.Contains(got, r) {
					t.Errorf("masked output dropped real code %q:\n%s", r, got)
				}
			}
		})
	}
}

func TestMaskStrings_KeepsCommentsBlanksStrings(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		gone   []string // lived in a string → blanked
		remain []string // a comment directive or real code → kept
	}{
		{
			name:   "line comment survives, string blanked",
			src:    `x := f() //nolint:errcheck` + "\n" + `s := "//nolint here"`,
			gone:   []string{"//nolint here"},
			remain: []string{"//nolint:errcheck", "x := f()"},
		},
		{
			name:   "hash comment survives",
			src:    "y = g()  # type: ignore",
			gone:   nil,
			remain: []string{"# type: ignore", "y = g()"},
		},
		{
			name:   "block comment survives",
			src:    "a() /* eslint-disable */ b()",
			gone:   nil,
			remain: []string{"eslint-disable", "a()", "b()"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := maskStrings(c.src)
			if len(got) != len(c.src) {
				t.Fatalf("maskStrings changed length: got %d, want %d", len(got), len(c.src))
			}
			for _, g := range c.gone {
				if strings.Contains(got, g) {
					t.Errorf("maskStrings left string content %q:\n%s", g, got)
				}
			}
			for _, r := range c.remain {
				if !strings.Contains(got, r) {
					t.Errorf("maskStrings dropped %q (comment or code):\n%s", r, got)
				}
			}
		})
	}
}

func TestMask_PreservesNewlines(t *testing.T) {
	src := "line1 // c\nline2"
	got := mask(src)
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("mask did not preserve newline: %q", got)
	}
}
