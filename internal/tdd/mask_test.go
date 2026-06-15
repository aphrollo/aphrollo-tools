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

func TestMask_PreservesNewlines(t *testing.T) {
	src := "line1 // c\nline2"
	got := mask(src)
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("mask did not preserve newline: %q", got)
	}
}
