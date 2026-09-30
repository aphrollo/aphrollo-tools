package mask

import (
	"strings"
	"testing"
)

// sp is n spaces: what a blanked run of n bytes leaves.
func sp(n int) string { return strings.Repeat(" ", n) }

// A PHP attribute opens with `#[`, which is code, while every other `#` still
// opens a comment.
func TestLex_PHPAttributesAreCodeAndOtherHashesAreComments(t *testing.T) {
	runLexCases(t, "php", map[string]lexCase{
		"an attribute stays":                     {"#[Test]\n$a = 1;", "#[Test]\n$a = 1;"},
		"a string in an attribute is blanked":    {"#[Route(\"/a\")] # c", "#[Route(\"  \")]    "},
		"a quote in a real comment opens none":   {"$a = 1; # it's\n$b = 'c';", "$a = 1;       \n$b = ' ';"},
		"a hash and a space is a comment":        {"$a; # [x]\n", "$a;      \n"},
		"a hash before a bracket on its own":     {"# [x]\n", "     \n"},
		"an attribute opener at the very end":    {"x #[", "x #["},
		"a hash at the very end is a comment":    {"x #", "x  "},
		"an attribute then a slash comment":      {"#[A] // it's\n'q'", "#[A]" + sp(8) + "\n' '"},
		"two attributes on one line":             {"#[A, B] #[C]", "#[A, B] #[C]"},
		"an attribute inside a block comment":    {"/* #[A] 'x' */ 'y'", "               ' '"},
		"a string holding an attribute opener":   {"$s = '#[A]';", "$s = '    ';"},
		"an attribute opener inside a heredoc":   {"<<<E\n#[A]\nE;", "<<<E\n    \nE;"},
		"a comment after an attribute opener":    {"#[A]\n# c\n", "#[A]\n   \n"},
		"a hash directly after an opener's line": {"#[A]#c", "#[A]  "},
	})
}

func TestLex_PHPHeredocsAndNowdocsAreStrings(t *testing.T) {
	runLexCases(t, "php", map[string]lexCase{
		"a heredoc body is blanked":             {"$s = <<<EOT\n  a 'b' // c\nEOT;\n$t = 'x';", "$s = <<<EOT\n            \nEOT;\n$t = ' ';"},
		"a nowdoc":                              {"<<<'EOT'\nbody $x\nEOT;\n", "<<<'EOT'\n       \nEOT;\n"},
		"a quoted heredoc":                      {"<<<\"EOT\"\nbody\nEOT;\n", "<<<\"EOT\"\n    \nEOT;\n"},
		"blanks between the operator and id":    {"<<<  EOT\nbody\nEOT;\n", "<<<  EOT\n    \nEOT;\n"},
		"an indented closer":                    {"<<<EOT\n  text\n  EOT;\n", "<<<EOT\n" + sp(6) + "\n  EOT;\n"},
		"a longer word is not the closer":       {"<<<EOT\nEOTX stays body\nEOT\n", "<<<EOT\n               \nEOT\n"},
		"the closer inside a line is body":      {"<<<EOT\nsee EOT here\nEOT;", "<<<EOT\n            \nEOT;"},
		"a closer followed by a bracket":        {"f(<<<EOT\nx\nEOT);\n'q'", "f(<<<EOT\n \nEOT);\n' '"},
		"a closer followed by a comma":          {"f(<<<EOT\nx\nEOT, 'a');", "f(<<<EOT\n \nEOT, ' ');"},
		"another identifier's line is body":     {"<<<A\nB\nA", "<<<A\n \nA"},
		"an unclosed heredoc runs to the end":   {"<<<EOT\nabc", "<<<EOT\n   "},
		"an empty heredoc":                      {"<<<EOT\nEOT;\n'q'", "<<<EOT\nEOT;\n' '"},
		"two heredocs in a row":                 {"<<<A\nx\nA;\n<<<B\ny\nB;\n", "<<<A\n \nA;\n<<<B\n \nB;\n"},
		"a CRLF heredoc":                        {"<<<EOT\r\nbody\r\nEOT;", "<<<EOT\r\n     \nEOT;"},
		"an identifier with digits":             {"<<<E_1\nx\nE_1;", "<<<E_1\n \nE_1;"},
		"a hash in the body is no comment":      {"<<<EOT\n# it's\nEOT;\n'q'", "<<<EOT\n      \nEOT;\n' '"},
		"a bitshift-like operator is code":      {"$a <<< 1; $b = 'x';", "$a <<< 1; $b = ' ';"},
		"no identifier after the operator":      {"<<<\nx 'y'\n", "<<<\nx ' '\n"},
		"a digit cannot start the identifier":   {"<<<1A\nx 'y'\n1A", "<<<1A\nx ' '\n1A"},
		"the head must end its line":            {"<<<EOT;\nx 'y'\nEOT;", "<<<EOT;\nx ' '\nEOT;"},
		"a head with no line end is code":       {"<<<EOT", "<<<EOT"},
		"an operator ending the input":          {"x <<<", "x <<<"},
		"an operator and blanks ending it":      {"x <<<  ", "x <<<  "},
		"a quoted head cut by the input":        {"<<<'EOT", "<<<'" + sp(3)},
		"a zero cannot start the identifier":    {"<<<0A\nx 'y'\n0A", "<<<0A\nx ' '\n0A"},
		"a nine cannot start the identifier":    {"<<<9A\nx 'y'\n9A", "<<<9A\nx ' '\n9A"},
		"every identifier byte at its bounds":   {"<<<_0_9_a_z_A_Z\nx\n_0_9_a_z_A_Z;\n'q'", "<<<_0_9_a_z_A_Z\n \n_0_9_a_z_A_Z;\n' '"},
		"a byte past the bounds ends the id":    {"<<<A/\nx 'y'\n", "<<<A/\nx ' '\n"},
		"a lowercase id with a closer after it": {"<<<z\nb\nz;", "<<<z\n \nz;"},
		"a quoted id needs its closing quote":   {"<<<'EOT\nx 'y'\n", "<<<'" + sp(3) + "\n" + sp(2) + "'y'\n"},
		"a quoted id must match its quote":      {"<<<'EOT\"\nx\n", "<<<'" + sp(4) + "\n \n"},
		"a heredoc after a line comment":        {"// <<<EOT\n'q'", sp(9) + "\n' '"},
		"a heredoc inside a string is text":     {"$s = '<<<EOT';\n'q'", "$s = '      ';\n' '"},
		"the closer may be the last bytes":      {"<<<EOT\nx\nEOT", "<<<EOT\n \nEOT"},
		"the closer after tabs":                 {"<<<EOT\nx\n\t\tEOT;", "<<<EOT\n \n\t\tEOT;"},
		"a closer line with a longer id prefix": {"<<<EO\nEOT\nEO;", "<<<EO\n   \nEO;"},
	})
}

// A heredoc is a string: the comment-preserving view blanks its body and the
// view that blanks nothing in strings leaves it.
func TestLex_PHPHeredocBodyFollowsTheStringView(t *testing.T) {
	src := "<<<EOT\n# c 'q'\nEOT;\n# d\n"
	if got, want := lexAs(t, "php", src, true, false), "<<<EOT\n       \nEOT;\n# d\n"; got != want {
		t.Errorf("strings blanked, comments kept:\n got %q\nwant %q", got, want)
	}
	if got, want := lexAs(t, "php", src, false, true), "<<<EOT\n# c 'q'\nEOT;\n   \n"; got != want {
		t.Errorf("strings kept, comments blanked:\n got %q\nwant %q", got, want)
	}
}

// line_except names the openers that look like a line marker and are code.
func TestLex_ALineExceptionIsCodeWhereTheMarkerWouldOpenAComment(t *testing.T) {
	lx := synth(t, "name = \"x\"\n[comments]\nline = [\"#\", \"--\"]\nline_except = [\"#!\", \"--[\"]\n[string.q]\nopen = \"'\"\n")
	cases := map[string]string{
		"#!shebang 'a'\n":   "#!shebang ' '\n",
		"# c 'a'\n'b'":      sp(7) + "\n' '",
		"--[ code 'a']\n":   "--[ code ' ']\n",
		"-- c 'a'\n'b'":     sp(8) + "\n' '",
		"#\n'b'":            " \n' '",
		"x #! y # it's\n":   "x #! y " + sp(6) + "\n",
		"a -- b --[ c\n'b'": "a " + sp(10) + "\n' '",
	}
	for src, want := range cases {
		if got := lx.Lex(src, true, true); got != want {
			t.Errorf("Lex(%q):\n got %q\nwant %q", src, got, want)
		}
	}
}
