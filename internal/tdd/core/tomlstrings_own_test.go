package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These pin tomlstrings.go's line-scanner TOML reader: every branch is
// exercised only through higher packages that read a real aphrollo.toml
// today, so a mutant on the scanner itself survives P's own suite. Tests
// here call the unexported functions directly.

func writeTomlFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "aphrollo.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTomlStringsIn_ReadsDedupedSortedArray(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		body  string
		table string
		key   string
		want  []string
	}{
		{
			name:  "single line array",
			body:  "[aphrollo]\naccept = [\"a\", \"b\"]\n",
			table: "[aphrollo]",
			key:   "accept",
			want:  []string{"a", "b"},
		},
		{
			name:  "multi-line array",
			body:  "[aphrollo]\naccept = [\n  \"z\",\n  \"a\",\n]\n",
			table: "[aphrollo]",
			key:   "accept",
			want:  []string{"a", "z"}, // sorted
		},
		{
			name:  "duplicates deduped",
			body:  "[aphrollo]\naccept = [\"a\", \"a\", \"b\"]\n",
			table: "[aphrollo]",
			key:   "accept",
			want:  []string{"a", "b"},
		},
		{
			name:  "absent key returns nil",
			body:  "[aphrollo]\nother = [\"a\"]\n",
			table: "[aphrollo]",
			key:   "accept",
			want:  nil,
		},
		{
			name:  "wrong table ignored",
			body:  "[other]\naccept = [\"a\"]\n",
			table: "[aphrollo]",
			key:   "accept",
			want:  nil,
		},
		{
			// issue #139: a quoted entry that itself mentions ']' must not
			// close the array early and drop every entry after it.
			name:  "quoted bracket inside entry does not close array early",
			body:  "[aphrollo]\naccept = [\"start indexes '[' and end indexes ']'\", \"after\"]\n",
			table: "[aphrollo]",
			key:   "accept",
			want:  []string{"after", "start indexes '[' and end indexes ']'"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeTomlFixture(t, c.body)
			got := tomlStringsIn(path, c.table, c.key)
			if len(got) != len(c.want) {
				t.Fatalf("tomlStringsIn = %#v, want %#v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("tomlStringsIn = %#v, want %#v", got, c.want)
				}
			}
		})
	}
	t.Run("unreadable manifest returns nil", func(t *testing.T) {
		t.Parallel()
		if got := tomlStringsIn(filepath.Join(t.TempDir(), "missing.toml"), "[aphrollo]", "accept"); got != nil {
			t.Fatalf("want nil for unreadable manifest, got %#v", got)
		}
	})
}

func TestTomlArrayCommaError_ReportsAdjacentEntriesWithNoComma(t *testing.T) {
	t.Parallel()

	t.Run("unreadable manifest is nil", func(t *testing.T) {
		t.Parallel()
		if err := tomlArrayCommaError(filepath.Join(t.TempDir(), "missing.toml"), "[aphrollo]", "accept"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("key absent is nil", func(t *testing.T) {
		t.Parallel()
		path := writeTomlFixture(t, "[aphrollo]\nother = [\"a\"]\n")
		if err := tomlArrayCommaError(path, "[aphrollo]", "accept"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("zero elements is nil", func(t *testing.T) {
		t.Parallel()
		path := writeTomlFixture(t, "[aphrollo]\naccept = []\n")
		if err := tomlArrayCommaError(path, "[aphrollo]", "accept"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("one element is nil", func(t *testing.T) {
		t.Parallel()
		path := writeTomlFixture(t, "[aphrollo]\naccept = [\"a\"]\n")
		if err := tomlArrayCommaError(path, "[aphrollo]", "accept"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("properly separated elements is nil", func(t *testing.T) {
		t.Parallel()
		path := writeTomlFixture(t, "[aphrollo]\naccept = [\"a\", \"b\", \"c\"]\n")
		if err := tomlArrayCommaError(path, "[aphrollo]", "accept"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("missing comma is reported with both entries and the file name", func(t *testing.T) {
		t.Parallel()
		path := writeTomlFixture(t, "[aphrollo]\naccept = [\"a\" \"b\"]\n")
		err := tomlArrayCommaError(path, "[aphrollo]", "accept")
		if err == nil {
			t.Fatal("want an error for two adjacent entries with no comma")
		}
		msg := err.Error()
		if !strings.Contains(msg, "aphrollo.toml") || !strings.Contains(msg, `"a"`) || !strings.Contains(msg, `"b"`) {
			t.Fatalf("error missing file/entries: %s", msg)
		}
	})
	t.Run("multi-line missing comma is still caught", func(t *testing.T) {
		t.Parallel()
		path := writeTomlFixture(t, "[aphrollo]\naccept = [\n  \"a\"\n  \"b\",\n]\n")
		if err := tomlArrayCommaError(path, "[aphrollo]", "accept"); err == nil {
			t.Fatal("want an error across a line break with no comma")
		}
	})
}

func TestFirstUnseparatedArrayEntries_FindsTheFirstAdjacentPair(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		body          string
		wantMalformed bool
		wantBefore    string
		wantAfter     string
	}{
		{name: "empty", body: "[]", wantMalformed: false},
		{name: "one entry", body: `["a"]`, wantMalformed: false},
		{name: "two separated", body: `["a", "b"]`, wantMalformed: false},
		{name: "two adjacent", body: `["a" "b"]`, wantMalformed: true, wantBefore: "a", wantAfter: "b"},
		{name: "unterminated string swallows the rest", body: `["a`, wantMalformed: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, after, malformed := firstUnseparatedArrayEntries(c.body)
			if malformed != c.wantMalformed {
				t.Fatalf("malformed = %v, want %v", malformed, c.wantMalformed)
			}
			if malformed && (before != c.wantBefore || after != c.wantAfter) {
				t.Fatalf("before/after = %q/%q, want %q/%q", before, after, c.wantBefore, c.wantAfter)
			}
		})
	}
}

func TestStripQuoted_RemovesQuotedRunsOnly(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`abc`, `abc`},
		{`a"b"c`, `ac`},
		{`["x", "y"]`, `[, ]`},
		// An unterminated quote consumes the rest of the line as string
		// content; nothing after it is punctuation either.
		{`a"unterminated`, `a`},
	}
	for _, c := range cases {
		if got := stripQuoted(c.in); got != c.want {
			t.Errorf("stripQuoted(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuotedWords_ExtractsQuotedContentInOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{`"a", "b"`, []string{"a", "b"}},
		{`no quotes here`, nil},
		{`"only one`, nil}, // unterminated: nothing extracted
		{`"a", "unterminated`, []string{"a"}},
	}
	for _, c := range cases {
		got := quotedWords(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("quotedWords(%q) = %#v, want %#v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("quotedWords(%q) = %#v, want %#v", c.in, got, c.want)
			}
		}
	}
}

func TestBasicStringBody_UnescapesAndFindsTheClosingQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		in        string
		wantBody  string
		wantEnd   int
		wantFound bool
	}{
		{name: "plain", in: `abc"rest`, wantBody: "abc", wantEnd: 3, wantFound: true},
		{name: "escaped quote kept as quote", in: `a\"b"rest`, wantBody: `a"b`, wantEnd: 4, wantFound: true},
		{name: "escaped backslash kept as backslash", in: `a\\b"rest`, wantBody: `a\b`, wantEnd: 4, wantFound: true},
		// Any other escape is kept AS WRITTEN — backslash and following char
		// both survive — so a reason quoting code (`if x == \"\"`) never
		// silently loses the escape it did not ask for.
		{name: "unknown escape kept verbatim", in: `a\nb"rest`, wantBody: `a\nb`, wantEnd: 4, wantFound: true},
		{name: "unterminated", in: `no closing quote`, wantBody: "", wantEnd: 0, wantFound: false},
		{name: "trailing backslash unterminated", in: `abc\`, wantBody: "", wantEnd: 0, wantFound: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, end, ok := basicStringBody(c.in)
			if ok != c.wantFound {
				t.Fatalf("ok = %v, want %v", ok, c.wantFound)
			}
			if !ok {
				return
			}
			if body != c.wantBody || end != c.wantEnd {
				t.Fatalf("basicStringBody(%q) = (%q, %d), want (%q, %d)", c.in, body, end, c.wantBody, c.wantEnd)
			}
		})
	}
}
