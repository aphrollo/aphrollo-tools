package suite

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These are suite's own tests of mutants_config_suite.go, mutants_lanebase_suite.go,
// precommit_diffscan_suite.go's declaresTest and go_test_reach.go's
// goTestReachingPackages, reached today only through other packages.

// TestFirstDeclaredList_TakesTheFirstTableThatDeclaresANonEmptyList pins the
// precedence: the first table with a non-empty array wins, later tables and
// empty arrays are skipped, and none is nil.
func TestFirstDeclaredList_TakesTheFirstTableThatDeclaresANonEmptyList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "first.toml", "[cfg]\nlist = []\n")
	write(t, dir, "second.toml", "[cfg]\nlist = [\"b\", \"a\"]\n")
	write(t, dir, "third.toml", "[cfg]\nlist = [\"never\"]\n")
	tables := []mutantsConfigTable{
		{filepath.Join(dir, "absent.toml"), "[cfg]"},
		{filepath.Join(dir, "first.toml"), "[cfg]"},
		{filepath.Join(dir, "second.toml"), "[cfg]"},
		{filepath.Join(dir, "third.toml"), "[cfg]"},
	}
	if got := firstDeclaredList(tables, "list"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("firstDeclaredList = %v, want the second table's [a b]", got)
	}
	if got := firstDeclaredList(tables, "unset"); got != nil {
		t.Fatalf("firstDeclaredList of an undeclared key = %v, want nil", got)
	}
	if got := firstDeclaredList(nil, "list"); got != nil {
		t.Fatalf("firstDeclaredList of no tables = %v, want nil", got)
	}
}

// TestMutantsConfigTables_TriesTheCargoSpellingBeforeAphrolloToml pins the two
// tables and their order, rooted at the workspace root for Cargo.toml and at
// the repo root for aphrollo.toml.
func TestMutantsConfigTables_TriesTheCargoSpellingBeforeAphrolloToml(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = []\n")
	got := mutantsConfigTables(root)
	want := []mutantsConfigTable{
		{filepath.Join(root, "Cargo.toml"), "[workspace.metadata.aphrollo]"},
		{filepath.Join(root, "aphrollo.toml"), "[aphrollo]"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutantsConfigTables = %v, want %v", got, want)
	}
}

// TestMutantsConfigTables_ARootWithNoCargoWorkspaceUsesItselfForBoth pins the
// fallback: with no workspace the Cargo spelling is looked for at the root.
func TestMutantsConfigTables_ARootWithNoCargoWorkspaceUsesItselfForBoth(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	got := mutantsConfigTables(root)
	if len(got) != 2 || got[0].Path != filepath.Join(root, "Cargo.toml") || got[1].Path != filepath.Join(root, "aphrollo.toml") {
		t.Fatalf("mutantsConfigTables = %v, want both paths under %s", got, root)
	}
}

// TestDeclaresTest_RecognisesADeclarationPerLanguage pins the table: each
// supported extension's declaration shapes, and that an assertion inside an
// existing test is not a declaration.
func TestDeclaresTest_RecognisesADeclarationPerLanguage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ext, line string
		want      bool
	}{
		{".go", "func TestWidget(t *testing.T) {", true},
		{".go", "func BenchmarkWidget(b *testing.B) {", true},
		{".go", `	t.Run("sub", func(t *testing.T) {`, true},
		{".go", "func helper() {}", false},
		{".go", "	if got != want {", false},
		{".rs", "#[test]", true},
		{".rs", "#[tokio::test]", true},
		{".rs", "fn plain() {}", false},
		{".py", "def test_widget():", true},
		{".py", "async def test_widget():", true},
		{".py", "def helper():", false},
		{".zig", `test "widget" {`, true},
		{".zig", "fn helper() void {", false},
	}
	for _, c := range cases {
		decl, known := declaresTest(c.ext, c.line)
		if !known || decl != c.want {
			t.Errorf("declaresTest(%q, %q) = (%v, %v), want (%v, true)", c.ext, c.line, decl, known, c.want)
		}
	}
}

// TestDeclaresTest_TheLanguageTableKeepsEveryShapeTheHardCodedTableRead is the
// behaviour parity for moving the declaration shapes into the language rows:
// each line is judged as the per-extension regexes it replaced judged it.
func TestDeclaresTest_TheLanguageTableKeepsEveryShapeTheHardCodedTableRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ext, line string
		want      bool
	}{
		{".go", "func Test(t *testing.T) {", true},
		{".go", "func TestWidget(t *testing.T) {", true},
		{".go", "func TestWidget (t *testing.T) {", true},
		{".go", "func  TestWidget(t *testing.T) {", true},
		{".go", "\tfunc TestIndented(t *testing.T) {", true},
		{".go", "func TestMainLoop(t *testing.T) {", true},
		{".go", "func TestMai(t *testing.T) {", true},
		{".go", "func TestM(t *testing.T) {", true},
		{".go", "func Test_x(t *testing.T) {", true},
		{".go", "func BenchmarkWidget(b *testing.B) {", true},
		{".go", "func FuzzWidget(f *testing.F) {", true},
		{".go", "func ExampleWidget() {", true},
		{".go", `	t.Run("sub", func(t *testing.T) {`, true},
		{".go", `x := t.Run("sub", f)`, true},
		{".go", "func TestMain(m *testing.M) {", false},
		{".go", "func TestMain (m *testing.M) {", false},
		{".go", "\tfunc TestMain(m *testing.M) {", false},
		{".go", "func testWidget(t *testing.T) {", false},
		{".go", "func (s *S) TestWidget(t *testing.T) {", false},
		{".go", "func Widget() {", false},
		{".go", "// func TestCommented(", false},
		{".go", "t.Runner()", false},
		{".rs", "#[test]", true},
		{".rs", "    #[test]", true},
		{".rs", "#[ test ]", true},
		{".rs", "#[tokio::test]", true},
		{".rs", "#[tokio::test(flavor = \"multi_thread\")]", true},
		{".rs", "#[rstest]", true},
		{".rs", "#[rstest::rstest]", true},
		{".rs", "#[test_case(1, 2)]", true},
		{".rs", "#[testing]", false},
		{".rs", "#[cfg(test)]", false},
		{".rs", "fn test_thing() {", false},
		{".py", "def test_widget():", true},
		{".py", "    def test_widget(self):", true},
		{".py", "async def test_widget():", true},
		{".py", "  async  def test_widget():", true},
		{".py", "def testwidget():", false},
		{".py", "def helper_test_x():", false},
		{".zig", `test "widget" {`, true},
		{".zig", "test {", true},
		{".zig", "  test \"indented\" {", true},
		{".zig", "test widget {", false},
		{".zig", "fn helper() void {", false},
		{".js", `it("does a thing", () => {`, true},
		{".js", `  test("does a thing", () => {`, true},
		{".js", `describe("a suite", () => {`, true},
		{".js", `it.only("x", () => {`, true},
		{".js", `test.each([1, 2])("x", () => {`, true},
		{".js", `it ("spaced", () => {`, true},
		{".js", `expect(it).toBe(1)`, false},
		{".js", `waitit("x")`, false},
		{".jsx", `it("x", () => {`, true},
		{".mjs", `it("x", () => {`, true},
		{".cjs", `it("x", () => {`, true},
		{".ts", `it("x", () => {`, true},
		{".tsx", `describe("x", () => {`, true},
		{".mts", `test("x", () => {`, true},
		{".cts", `test("x", () => {`, true},
		{".ts", `const x = 1`, false},
	}
	for _, c := range cases {
		decl, known := declaresTest(c.ext, c.line)
		if !known || decl != c.want {
			t.Errorf("declaresTest(%q, %q) = (%v, %v), want (%v, true)", c.ext, c.line, decl, known, c.want)
		}
	}
}

// TestDeclaresTest_ALanguageWithNoDeclarationShapeIsUnknown pins that a row
// which lists no declaration pattern stays unknown, so fail-first keeps
// erring toward running the suite for it.
func TestDeclaresTest_ALanguageWithNoDeclarationShapeIsUnknown(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".java", ".kt", ".cs", ".php", ".rb", ".sh", ".yaml", ".toml", ".md", ""} {
		if decl, known := declaresTest(ext, "@Test void x() {}"); decl || known {
			t.Errorf("declaresTest(%q) = (%v, %v), want (false, false)", ext, decl, known)
		}
	}
}

// TestGoTestFuncNames_ReadsTheGoRowsTestPatterns pins the Go names a staged
// file contributes to the -run filter: every Test function, in declaration
// order, with TestMain, helpers, methods and other declarations left out.
func TestGoTestFuncNames_ReadsTheGoRowsTestPatterns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "x_test.go", "package x\n\nfunc TestMain(m *testing.M) {}\nfunc TestWear(t *testing.T) {}\nfunc  TestGrip (t *testing.T) {}\n"+
		"func BenchmarkWear(b *testing.B) {}\nfunc FuzzWear(f *testing.F) {}\nfunc testHelper() {}\nfunc (s S) TestMethod() {}\n"+
		"func TestMainLoop(t *testing.T) {}\nfunc Test_x(t *testing.T) {}\n  func TestIndented() {}\n")
	got := strings.Join(goTestFuncNames(root, "x_test.go"), ",")
	if want := "TestWear,TestGrip,TestMainLoop,Test_x"; got != want {
		t.Errorf("goTestFuncNames = %s, want %s", got, want)
	}
	if names := goTestFuncNames(root, "absent_test.go"); len(names) != 0 {
		t.Errorf("an unreadable file names %v", names)
	}
}

// TestDeclaresTest_AGoTestMainIsNotATest pins the exemption: TestMain has a
// test's name and none of its meaning.
func TestDeclaresTest_AGoTestMainIsNotATest(t *testing.T) {
	t.Parallel()
	decl, known := declaresTest(".go", "func TestMain(m *testing.M) {")
	if decl || !known {
		t.Fatalf("declaresTest = (%v, %v), want (false, true)", decl, known)
	}
}

// TestDeclaresTest_AnUnsupportedExtensionIsUnknown pins the third answer.
func TestDeclaresTest_AnUnsupportedExtensionIsUnknown(t *testing.T) {
	t.Parallel()
	if decl, known := declaresTest(".md", "func TestX("); decl || known {
		t.Fatalf("declaresTest = (%v, %v), want (false, false)", decl, known)
	}
}

// Serial: swaps the package's go reach-graph probe, a process-wide override.
// TestGoTestReachingPackages_IsTheGraphsReachOfTheDirectory pins the query: the
// directory itself and everything whose test binary reaches it, sorted.
func TestGoTestReachingPackages_IsTheGraphsReachOfTheDirectory(t *testing.T) {
	t.Cleanup(SetGoReachGraphForTest(func(string) (goReachGraph, error) {
		return goReachGraph{edges: map[string][]string{"top": {"mid"}, "mid": {"leaf"}}}, nil
	}))
	got, err := goTestReachingPackages("/root", "leaf")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"leaf", "mid", "top"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("goTestReachingPackages = %v, want %v", got, want)
	}
}

// Serial: swaps the package's go reach-graph probe, a process-wide override.
// TestGoTestReachingPackages_AnUnreadableGraphIsAnError pins the failure arm.
func TestGoTestReachingPackages_AnUnreadableGraphIsAnError(t *testing.T) {
	boom := errors.New("go list failed")
	t.Cleanup(SetGoReachGraphForTest(func(string) (goReachGraph, error) { return goReachGraph{}, boom }))
	if _, err := goTestReachingPackages("/root", "leaf"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the graph's error", err)
	}
}

// commitOn makes one commit that adds file with content and returns its sha.
func commitOn(t *testing.T, repo, file string) string {
	t.Helper()
	write(t, repo, file, file+"\n")
	gitDo(t, repo, "add", file)
	gitDo(t, repo, "commit", "-qm", "add "+file)
	return gitOut(repo, "rev-parse", "HEAD")
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestIsAncestorCommit_IsStrictAncestry pins the predicate: an ancestor is one,
// a commit is not its own ancestor, and a descendant is not an ancestor.
func TestIsAncestorCommit_IsStrictAncestry(t *testing.T) {
	repo := makeGoRepo(t)
	first := gitOut(repo, "rev-parse", "HEAD")
	second := commitOn(t, repo, "two.go")
	if !isAncestorCommit(repo, first, second) {
		t.Error("the first commit is an ancestor of the second")
	}
	if isAncestorCommit(repo, second, first) {
		t.Error("the second commit is not an ancestor of the first")
	}
	if isAncestorCommit(repo, first, first) {
		t.Error("a commit is not its own strict ancestor")
	}
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestLaneBaseSHA_IsTheNewestMergeBaseAmongTheTrunkCandidates pins the choice:
// of the trunk refs that exist, the merge-base that is a descendant of the
// others wins, whichever ref names it.
func TestLaneBaseSHA_IsTheNewestMergeBaseAmongTheTrunkCandidates(t *testing.T) {
	repo := makeGoRepo(t)
	c1 := gitOut(repo, "rev-parse", "HEAD")
	gitDo(t, repo, "branch", "-m", "lane")
	c2 := commitOn(t, repo, "two.go")
	commitOn(t, repo, "three.go")

	gitDo(t, repo, "branch", "main", c1)
	gitDo(t, repo, "branch", "master", c2)
	if got := laneBaseSHA(repo); got != c2 {
		t.Fatalf("laneBaseSHA = %q, want the newer base %q (master)", got, c2)
	}
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestLaneBaseSHA_AnOlderLaterCandidateDoesNotReplaceANewerBase pins the other
// order: main names the newer base and master the older one, and main stays.
func TestLaneBaseSHA_AnOlderLaterCandidateDoesNotReplaceANewerBase(t *testing.T) {
	repo := makeGoRepo(t)
	c1 := gitOut(repo, "rev-parse", "HEAD")
	gitDo(t, repo, "branch", "-m", "lane")
	c2 := commitOn(t, repo, "two.go")
	commitOn(t, repo, "three.go")

	gitDo(t, repo, "branch", "main", c2)
	gitDo(t, repo, "branch", "master", c1)
	if got := laneBaseSHA(repo); got != c2 {
		t.Fatalf("laneBaseSHA = %q, want %q (main's base is the newer one)", got, c2)
	}
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestLaneBaseSHA_WithNoTrunkRefFallsBackToTheParentCommit pins the fallback: a
// repo with none of the candidate refs measures against HEAD's parent.
func TestLaneBaseSHA_WithNoTrunkRefFallsBackToTheParentCommit(t *testing.T) {
	repo := makeGoRepo(t)
	first := gitOut(repo, "rev-parse", "HEAD")
	gitDo(t, repo, "branch", "-m", "lane-only")
	commitOn(t, repo, "two.go")
	if got := laneBaseSHA(repo); got != first {
		t.Fatalf("laneBaseSHA = %q, want HEAD~1 %q", got, first)
	}
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestLaneBaseSHA_OutsideAnyRepositoryIsEmpty pins the unreadable arm.
func TestLaneBaseSHA_OutsideAnyRepositoryIsEmpty(t *testing.T) {
	if got := laneBaseSHA(t.TempDir()); strings.TrimSpace(got) != "" {
		t.Fatalf("laneBaseSHA = %q, want none outside a repository", got)
	}
}
