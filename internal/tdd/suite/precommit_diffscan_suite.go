package suite

import (
	"regexp"

	langtable "github.com/aphrollo/aphrollo-tools/internal/lang"
)

// zigTestDeclRe recognises a Zig `test "name" {` or `test {` block. Zig has no
// row in the language table, so its one declaration shape is held here.
var zigTestDeclRe = regexp.MustCompile(`^\s*test\s+("|\{)`)

// declaresTest reports whether an added line declares a test in a file of
// extension ext (lower-cased, with its dot), and whether the language table
// knows the language's test declarations at all. The shapes are the
// `declarations` of the embedded language rows — declaration-shaped
// (attributes, test-func headers, subtest registrations), so an added
// assertion inside an existing test does not fire fail-first, matching its
// charter of proving NEW tests RED. A Go TestMain has a test's name and no
// test's meaning: it is the test binary's entry point, `go test -run` never
// selects it, and a proof that names it runs nothing, so the go row does not
// count it. It is the one judgment of "declares a test" every reader goes
// through.
func declaresTest(ext, line string) (decl, known bool) {
	if ext == ".zig" {
		return zigTestDeclRe.MatchString(line), true
	}
	tbl, err := langtable.Defaults()
	if err != nil {
		return false, false
	}
	return tbl.DeclaresTest(ext, line)
}
