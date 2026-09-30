package smell

import (
	"testing"

	langtable "github.com/aphrollo/aphrollo-tools/internal/lang"
)

// The directives are rows of the language table: a language that lists one is
// checked with no change to this package.
func TestSuppressed_TheTablesRowsNameTheDirectivesOfEveryLanguage(t *testing.T) {
	cases := []struct {
		name, path, src, kind string
		want                  bool
	}{
		{"go nolint", "a.go", "x() //nolint:errcheck\n", langtable.KindLint, true},
		{"python noqa", "a.py", "import x  # noqa\n", langtable.KindLint, true},
		{"python pylint", "a.py", "x = 1  # pylint: disable=invalid-name\n", langtable.KindLint, true},
		{"python flake8", "a.py", "x = 1  # flake8: noqa\n", langtable.KindLint, true},
		{"ruby rubocop", "a.rb", "x = 1 # rubocop:disable Style/Foo\n", langtable.KindLint, true},
		{"java suppress warnings", "A.java", "@SuppressWarnings(\"unchecked\")\nvoid f() {}\n", langtable.KindLint, true},
		{"java nopmd", "A.java", "int x; // NOPMD\n", langtable.KindLint, true},
		{"java checkstyle", "A.java", "// CHECKSTYLE:OFF\n", langtable.KindLint, true},
		{"csharp pragma", "A.cs", "#pragma warning disable CS0168\n", langtable.KindLint, true},
		{"csharp suppress message", "A.cs", "[SuppressMessage(\"a\", \"b\")]\n", langtable.KindLint, true},
		{"csharp assembly suppress message", "A.cs", "[assembly: SuppressMessage(\"a\", \"b\")]\n", langtable.KindLint, true},
		{"csharp resharper", "A.cs", "// ReSharper disable once All\n", langtable.KindLint, true},
		{"csharp coverage", "A.cs", "[ExcludeFromCodeCoverage]\nclass A {}\n", langtable.KindCoverage, true},
		{"kotlin suppress", "A.kt", "@Suppress(\"UNCHECKED_CAST\")\nfun f() {}\n", langtable.KindLint, true},
		{"kotlin file suppress", "A.kt", "@file:Suppress(\"x\")\n", langtable.KindLint, true},
		{"kotlin noinspection", "A.kt", "// noinspection Foo\n", langtable.KindLint, true},
		{"php phpcs ignore", "a.php", "// phpcs:ignore Generic\n", langtable.KindLint, true},
		{"php phpcs disable", "a.php", "// phpcs:disable\n", langtable.KindLint, true},
		{"php phpstan", "a.php", "// @phpstan-ignore-next-line\n", langtable.KindType, true},
		{"php psalm", "a.php", "/** @psalm-suppress MixedAssignment */\n", langtable.KindType, true},
		{"typescript ignore", "a.ts", "// @ts-ignore\n", langtable.KindType, true},
		{"typescript nocheck", "a.ts", "// @ts-nocheck\n", langtable.KindType, true},
		{"python type ignore", "a.py", "x = 1  # type: ignore\n", langtable.KindType, true},
		{"python pyright", "a.py", "x = 1  # pyright: ignore\n", langtable.KindType, true},
		{"istanbul", "a.js", "/* istanbul ignore next */\n", langtable.KindCoverage, true},
		{"c8", "a.js", "/* c8 ignore next */\n", langtable.KindCoverage, true},
		{"v8", "a.js", "/* v8 ignore next */\n", langtable.KindCoverage, true},
		{"python pragma", "a.py", "x = 1  # pragma: no cover\n", langtable.KindCoverage, true},
		{"ts expect error is admitted", "a.ts", "// @ts-expect-error\n", langtable.KindType, false},
		{"a directive of another kind", "a.py", "x = 1  # noqa\n", langtable.KindType, false},
		{"plain code", "A.java", "int x = 1;\n", langtable.KindLint, false},
		{"a directive quoted in a string", "A.java", "String s = \"@SuppressWarnings\";\n", langtable.KindLint, false},
		{"a directive in a text block", "A.java", "String s = \"\"\"\n  @SuppressWarnings\n  \"\"\";\n", langtable.KindLint, false},
		{"no such kind", "a.go", "x() //nolint\n", "style", false},
	}
	for _, c := range cases {
		v := newView(c.src, langOf(c.path))
		if got := suppressed(c.kind, v.directives); got != c.want {
			t.Errorf("%s: suppressed(%s) = %v, want %v", c.name, c.kind, got, c.want)
		}
	}
}

// A row's reason syntax admits a suppression that says why, and only within
// its own comment.
func TestSuppressed_AReasonAdmitsADirectiveWithinItsOwnComment(t *testing.T) {
	cases := []struct {
		name, src string
		want      bool
	}{
		{"bare line disable", "// eslint-disable-next-line no-x\n", true},
		{"described line disable", "// eslint-disable-next-line no-x -- generated code\n", false},
		{"two dashes need a description", "// eslint-disable-next-line no-x --\n", true},
		{"one dash is no separator", "// eslint-disable-next-line no-x - why\n", true},
		{"three dashes", "// eslint-disable-next-line no-x --- why\n", false},
		{"no space before the dashes", "// eslint-disable-next-line no-x-- why\n", true},
		{"described block disable", "/* eslint-disable no-x -- why */\n", false},
		{"a separator after the block closer is other text", "/* eslint-disable no-x */ // a -- b\n", true},
		{"a separator on the next line is other text", "// eslint-disable-next-line no-x\n// a -- b\n", true},
		{"a bare disable beside a described one", "// eslint-disable-next-line a -- why\n// eslint-disable-next-line b\n", true},
		{"two described disables", "// eslint-disable-next-line a -- why\n// eslint-disable-next-line b -- why\n", false},
	}
	for _, c := range cases {
		v := newView(c.src, langOf("a.js"))
		if got := suppressed(langtable.KindLint, v.directives); got != c.want {
			t.Errorf("%s: suppressed = %v, want %v", c.name, got, c.want)
		}
	}
}
