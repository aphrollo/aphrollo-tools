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

func TestMaskTokens_HashComment(t *testing.T) {
	// In a JS/TS file (#-is-code), the `#` must NOT make the lexer skip the rest
	// of the line, so the string still gets blanked and its content cannot leak.
	js := `this.#count = "use // nolint maybe"`
	got := maskTokens(js, true, false, false) // directives view, # not a comment
	if strings.Contains(got, "// nolint") {
		t.Errorf("# treated as comment in JS: directive leaked:\n%s", got)
	}
	if !strings.Contains(got, "this.#count") {
		t.Errorf("# not a comment should keep the private field intact:\n%s", got)
	}

	// In a Python file (#-is-comment), a `# type: ignore` directive stays visible
	// in the directives view (comments preserved) so it can be detected.
	py := `x = legacy()  # type: ignore`
	if got := maskTokens(py, true, false, true); !strings.Contains(got, "# type: ignore") {
		t.Errorf("python directive must survive the directives view:\n%s", got)
	}
}

func TestMask_PreservesNewlines(t *testing.T) {
	src := "line1 // c\nline2"
	got := mask(src)
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("mask did not preserve newline: %q", got)
	}
}
