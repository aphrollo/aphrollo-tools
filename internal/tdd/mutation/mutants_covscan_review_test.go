package mutation

import (
	"slices"
	"testing"
)

// A test-file variable that names os.Args starts the test binary again as much
// as a function does.
func TestScanPackage_ATestVariableThatNamesItsOwnBinarySeedsReexecTests(t *testing.T) {
	t.Parallel()
	tests := "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nvar self = os.Args[0]\n\n" +
		"func Test_Uses(t *testing.T) { _ = self }\n" +
		"func Test_Plain(t *testing.T) { _ = 1 }\n"
	scan, err := scanPackage(covscanWrite(t, map[string]string{"p.go": "package p\n", "p_test.go": tests}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := scan.reexecTests(); !slices.Equal(got, []string{"Test_Uses"}) {
		t.Fatalf("reexec tests = %v, want [Test_Uses]", got)
	}
}

// A helper of the external test package has a key of its own.
func TestScanPackage_ExternalTestPackageHelpersDoNotShadowTheSamePackage(t *testing.T) {
	t.Parallel()
	src := "package p\n\nfunc helper() int { return 1 }\n"
	xtest := "package p_test\n\nimport \"testing\"\n\nfunc helper() int { return 2 }\n\nfunc Test_X(t *testing.T) { _ = helper() }\n"
	scan, err := scanPackage(covscanWrite(t, map[string]string{"p.go": src, "x_test.go": xtest}), nil)
	if err != nil {
		t.Fatal(err)
	}
	prod, ok := scan.Funcs["helper"]
	if !ok || prod.Test {
		t.Fatalf("the production helper = %+v (found %v), want it kept under its own key", prod, ok)
	}
	if x, ok := scan.Funcs["x:helper"]; !ok || !x.Test {
		t.Fatalf("the external test helper = %+v (found %v), want key x:helper", x, ok)
	}
	if scan.TestKey["Test_X"] != "x:Test_X" {
		t.Fatalf("test key = %q", scan.TestKey["Test_X"])
	}
}

// A //go:embed directive sits in the doc comment, and an import is part of
// what every test is built from.
func TestScanPackage_DocCommentsAndImportsAreHashed(t *testing.T) {
	t.Parallel()
	base := "package p\n\nimport \"embed\"\n\n//go:embed a.txt\nvar f embed.FS\n\n//go:generate x\nfunc g() {}\n"
	a, _ := scanPackage(covscanWrite(t, map[string]string{"p.go": base}), nil)
	embed := "package p\n\nimport \"embed\"\n\n//go:embed b.txt\nvar f embed.FS\n\n//go:generate x\nfunc g() {}\n"
	b, _ := scanPackage(covscanWrite(t, map[string]string{"p.go": embed}), nil)
	if a.Rest == b.Rest {
		t.Error("an edited //go:embed directive did not change the non-function hash")
	}
	if a.Vars[0].Hash == b.Vars[0].Hash {
		t.Error("an edited //go:embed directive did not change the variable's hash")
	}
	doc := "package p\n\nimport \"embed\"\n\n//go:embed a.txt\nvar f embed.FS\n\n//go:generate y\nfunc g() {}\n"
	c, _ := scanPackage(covscanWrite(t, map[string]string{"p.go": doc}), nil)
	if a.Funcs["g"].Hash == c.Funcs["g"].Hash {
		t.Error("an edited doc comment of a function did not change its hash")
	}
	imp := "package p\n\nimport (\n\t\"embed\"\n\t\"os\"\n)\n\n//go:embed a.txt\nvar f embed.FS\n\n//go:generate x\nfunc g() { _ = os.Args }\n"
	d, _ := scanPackage(covscanWrite(t, map[string]string{"p.go": imp}), nil)
	if a.Rest == d.Rest {
		t.Error("an added import did not change the non-function hash")
	}
}

// Lines are the file's own, not the ones a //line directive names, and two
// blank variables do not share a key.
func TestScanPackage_LineDirectivesAndBlankVariables(t *testing.T) {
	t.Parallel()
	src := "package p\n\n//line other.go:100\nfunc g() {}\n\nvar _ = 1\n\nvar _ = 2\n"
	scan, err := scanPackage(covscanWrite(t, map[string]string{"p.go": src}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if g := scan.Funcs["g"]; g.Start != 4 {
		t.Fatalf("g starts at line %d, want the file's own line 4", g.Start)
	}
	if len(scan.Vars) != 2 || scan.Vars[0].Key == scan.Vars[1].Key {
		t.Fatalf("blank variables = %+v, want two distinct keys", scan.Vars)
	}
}
