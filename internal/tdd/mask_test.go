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

// TestMask_ZigMultilineString pins Zig's `\\`-prefixed multiline string literal.
// It is not a delimited string, so without explicit handling a smell token
// inside it stays visible to the detectors and can trip a FALSE edit-time block.
// Each `\\` line is a raw string running to end-of-line and must be blanked.
func TestMask_ZigMultilineString(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		gone   []string
		remain []string
	}{
		{
			// Smell tokens living inside a multiline string must not survive.
			name: "smells inside multiline string are blanked",
			src: "const q =\n" +
				"    \\\\ if (x == x) setTimeout(\n" +
				"    \\\\ try expectEqual(a, a);\n" +
				";",
			gone:   []string{"x == x", "setTimeout(", "expectEqual(a, a)"},
			remain: []string{"const q =", ";"},
		},
		{
			// Real code on a following line stays visible (per-line handling).
			name: "real code after the multiline string survives",
			src: "    \\\\ assert(a == a)\n" +
				"if (b == b) {}",
			gone:   []string{"a == a"},
			remain: []string{"if (b == b)"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mask(c.src)
			if len(got) != len(c.src) {
				t.Fatalf("mask changed length: got %d, want %d", len(got), len(c.src))
			}
			if strings.Count(got, "\n") != strings.Count(c.src, "\n") {
				t.Fatalf("mask did not preserve newlines: %q", got)
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

// TestMask_ZigCharLiteral pins that Zig char literals do not desync the lexer.
// `'\”` (an escaped single quote) must be consumed whole so a self-compare on
// the surrounding real code is still detectable, and a `'='` char literal must
// not swallow the rest of the line.
func TestMask_ZigCharLiteral(t *testing.T) {
	// `'\''` is an escaped-quote char literal; the surrounding `x == x` real
	// code must remain visible (the lexer must not lose sync).
	src := "if (x == x and c == '\\'') {}"
	got := mask(src)
	if len(got) != len(src) {
		t.Fatalf("mask changed length: got %d, want %d", len(got), len(src))
	}
	if !strings.Contains(got, "x == x") {
		t.Errorf("char literal desynced lexer, self-compare hidden:\n%s", got)
	}

	// A `'='` char literal must not desync the lexer either: code after it
	// (`y == y`) stays visible.
	src2 := "a = '=' ; if (y == y) {}"
	got2 := mask(src2)
	if !strings.Contains(got2, "y == y") {
		t.Errorf("'=' char literal desynced lexer, code after it hidden:\n%s", got2)
	}
}

// TestMask_BackslashEscapeNotMultiline guards the regression where a `\\`
// ESCAPE inside a regular "..." string is mistaken for a Zig multiline-string
// opener. The string branch consumes it first, so masking is identical to any
// other string and the trailing real code stays visible.
func TestMask_BackslashEscapeNotMultiline(t *testing.T) {
	src := `s := "a\\b"; if (z == z) {}`
	got := mask(src)
	if len(got) != len(src) {
		t.Fatalf("mask changed length: got %d, want %d", len(got), len(src))
	}
	if !strings.Contains(got, "if (z == z)") {
		t.Errorf("backslash escape spuriously started a multiline string, real code hidden:\n%s", got)
	}
	if !strings.Contains(got, "s :=") {
		t.Errorf("code before the string was clobbered:\n%s", got)
	}
}
