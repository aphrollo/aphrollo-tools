package mutation

import (
	"slices"
	"strings"
	"testing"
)

const covdiffProd = "package p\n\nimport \"fmt\"\n\nvar limit = 3\n\ntype T struct{}\n\nfunc (t *T) Run() int { return limit }\n\nfunc f() int {\n\treturn 1\n}\n\nfunc g() string { return fmt.Sprint(2) }\n"

const covdiffTests = "package p\n\nimport \"testing\"\n\nfunc helper() int { return 1 }\n\nfunc TestOne(t *testing.T) { _ = f() }\n\nfunc TestOneMore(t *testing.T) { _ = helper() }\n\nfunc TestTwo(t *testing.T) { _ = g() }\n"

func TestDiffFuncs_ProductionEdits(t *testing.T) {
	cases := []struct {
		name       string
		edit       func(string) string
		want       []string
		unmappable string
	}{
		{"a body edit maps to its function", func(s string) string { return strings.Replace(s, "return 1", "return 11", 1) }, []string{"f"}, ""},
		{"a method edit maps to Recv.Name", func(s string) string { return strings.Replace(s, "return limit", "return limit + 1", 1) }, []string{"T.Run"}, ""},
		{"two bodies edited map to both, sorted", func(s string) string {
			return strings.Replace(strings.Replace(s, "return 1", "return 11", 1), "Sprint(2)", "Sprint(3)", 1)
		}, []string{"f", "g"}, ""},
		{"a new function is an edited function", func(s string) string { return s + "\nfunc h() int { return 4 }\n" }, []string{"h"}, ""},
		{"a deleted function is an edited function", func(s string) string {
			return strings.Replace(s, "func g() string { return fmt.Sprint(2) }\n", "", 1)
		}, []string{"g"}, ""},
		{"a var edit is unmappable", func(s string) string { return strings.Replace(s, "limit = 3", "limit = 4", 1) }, nil, "non-function declaration"},
		{"a type edit is unmappable", func(s string) string { return strings.Replace(s, "struct{}", "struct{ n int }", 1) }, nil, "non-function declaration"},
		{"an import edit is unmappable", func(s string) string {
			return strings.Replace(s, "import \"fmt\"", "import (\n\t\"fmt\"\n\t\"os\"\n)", 1)
		}, nil, "non-function declaration"},
		{"a const added is unmappable", func(s string) string { return s + "\nconst k = 1\n" }, nil, "non-function declaration"},
		{"an init edit is unmappable", func(s string) string { return s + "\nfunc init() { limit = 5 }\n" }, nil, "init"},
		{"a package clause edit is unmappable", func(s string) string { return strings.Replace(s, "package p", "package q", 1) }, nil, "package clause"},
		{"a build constraint edit is unmappable", func(s string) string { return "//go:build linux\n\n" + s }, nil, "directive"},
		{"a comment-only edit changes no function", func(s string) string { return strings.Replace(s, "func f()", "// f is one.\nfunc f()", 1) }, nil, "no function changed"},
		{"a source that does not parse is unmappable", func(s string) string { return s + "\nfunc (" }, nil, "does not parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffFuncs([]byte(covdiffProd), []byte(tc.edit(covdiffProd)), false)
			if tc.unmappable != "" {
				if !strings.Contains(got.Unmappable, tc.unmappable) || len(got.Funcs) != 0 {
					t.Fatalf("got %+v, want unmappable with %q", got, tc.unmappable)
				}
				return
			}
			if got.Unmappable != "" || !slices.Equal(got.Funcs, tc.want) {
				t.Fatalf("got %+v, want funcs %v", got, tc.want)
			}
		})
	}
}

func TestDiffFuncs_TestFileEdits(t *testing.T) {
	both := []string{"TestOne", "TestOneMore", "TestTwo"}
	withThree := []string{"TestOne", "TestOneMore", "TestThree", "TestTwo"}
	cases := []struct {
		name       string
		old        string
		edit       func(string) string
		want       []string
		unmappable string
	}{
		{"a test body edit runs every test of the file", covdiffTests, func(s string) string { return strings.Replace(s, "_ = g()", "_ = g() + \"\"", 1) }, both, ""},
		{"a new test is included", covdiffTests, func(s string) string { return s + "\nfunc TestThree(t *testing.T) {}\n" }, withThree, ""},
		{"a helper edit is unmappable", covdiffTests, func(s string) string { return strings.Replace(s, "return 1 }", "return 2 }", 1) }, nil, "helper"},
		{"a plain import added with a new test is allowed", covdiffTests, func(s string) string {
			return strings.Replace(s, "import \"testing\"", "import (\n\t\"os\"\n\t\"testing\"\n)", 1) + "\nfunc TestThree(t *testing.T) { _ = os.Args }\n"
		}, withThree, ""},
		{"a blank import added is unmappable", covdiffTests, func(s string) string {
			return strings.Replace(s, "import \"testing\"", "import (\n\t_ \"embed\"\n\t\"testing\"\n)", 1)
		}, nil, "non-function declaration"},
		{"deleting every test leaves nothing to run", covdiffTests, func(string) string {
			return "package p\n\nimport \"testing\"\n\nfunc helper() int { return 1 }\n"
		}, nil, "declares no test"},
		{"TestMain is unmappable", covdiffTests, func(s string) string { return s + "\nfunc TestMain(m *testing.M) { m.Run() }\n" }, nil, "TestMain"},
		{"a var in the test file is unmappable", covdiffTests, func(s string) string { return s + "\nvar fixture = 1\n" }, nil, "non-function declaration"},
		{"a lowercase test name is a helper", covdiffTests, func(s string) string { return s + "\nfunc Testable() {}\n" }, nil, "helper"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffFuncs([]byte(tc.old), []byte(tc.edit(covdiffTests)), true)
			if tc.unmappable != "" {
				if !strings.Contains(got.Unmappable, tc.unmappable) {
					t.Fatalf("got %+v, want unmappable with %q", got, tc.unmappable)
				}
				return
			}
			if got.Unmappable != "" || !slices.Equal(got.Tests, tc.want) || !got.TestFile {
				t.Fatalf("got %+v, want tests %v", got, tc.want)
			}
		})
	}
}

func TestDiffFuncs_ANewTestFileOfTestsOnly(t *testing.T) {
	src := "package p\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n"
	got := DiffFuncs(nil, []byte(src), true)
	if got.Unmappable != "" || !slices.Equal(got.Tests, []string{"TestA", "TestB"}) {
		t.Fatalf("got %+v", got)
	}
}

// An autocrlf checkout holds the same file with other line endings than HEAD:
// that is not an edit, and must not read every function as changed (the
// function is f, as covdiffProd declares it).
func TestDiffFuncs_LineEndingsAreNotAnEdit(t *testing.T) {
	crlf := strings.ReplaceAll(covdiffProd, "\n", "\r\n")
	if got := DiffFuncs([]byte(covdiffProd), []byte(crlf), false); got.Unmappable == "" || len(got.Funcs) != 0 {
		t.Fatalf("the same file with CRLF endings: %+v, want no function changed", got)
	}
	edited := strings.Replace(crlf, "return 1", "return 11", 1)
	got := DiffFuncs([]byte(covdiffProd), []byte(edited), false)
	if got.Unmappable != "" || !slices.Equal(got.Funcs, []string{"f"}) {
		t.Fatalf("an edit in a CRLF checkout: %+v, want only f", got)
	}
}
