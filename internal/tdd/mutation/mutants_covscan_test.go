package mutation

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// covscanWrite lays files (name to source) into a fresh dir.
func covscanWrite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const covscanLib = `package p

type T struct{}

func (T) Method() int { return helper() }

func helper() int { return leaf() }

func leaf() int { return 1 }

func Unrelated() int { return 2 }

var table = map[string]func() int{"a": Unrelated}

var viaTable = table["a"]
`

const covscanTests = `package p

import "testing"

func Test_Direct(t *testing.T) { _ = leaf() }
func Test_Helper(t *testing.T) { _ = helper() }
func Test_MethodValue(t *testing.T) {
	f := T{}.Method
	_ = f()
}
func Test_Unrelated(t *testing.T) { _ = Unrelated() }
func Test_ViaTable(t *testing.T)  { _ = viaTable }
func Test_Reflect(t *testing.T)   { _ = "leaf" }
func TestMain(m *testing.M)       { m.Run() }
`

func TestScanPackage_ColdLookupFollowsHelpersAndMethodValues(t *testing.T) {
	dir := covscanWrite(t, map[string]string{"lib.go": covscanLib, "lib_test.go": covscanTests})
	scan, err := scanPackage(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := scan.candidateTests([]string{"leaf"})
	want := []string{"Test_Direct", "Test_Helper", "Test_MethodValue"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates for leaf = %v, want %v", got, want)
	}
}

func TestScanPackage_ColdLookupFollowsPackageVariables(t *testing.T) {
	dir := covscanWrite(t, map[string]string{"lib.go": covscanLib, "lib_test.go": covscanTests})
	scan, _ := scanPackage(dir, nil)
	got := scan.candidateTests([]string{"Unrelated"})
	want := []string{"Test_Unrelated", "Test_ViaTable"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates for Unrelated = %v, want %v", got, want)
	}
}

// The documented misses: reflection and a name only in a string are not seen,
// which is why a map built from the static lookup alone is partial.
func TestScanPackage_ColdLookupMissesReflectionAndStrings(t *testing.T) {
	dir := covscanWrite(t, map[string]string{"lib.go": covscanLib, "lib_test.go": covscanTests})
	scan, _ := scanPackage(dir, nil)
	if slices.Contains(scan.candidateTests([]string{"leaf"}), "Test_Reflect") {
		t.Fatal("a name inside a string must not count as a mention")
	}
}

func TestScanPackage_InterfaceDispatchOverApproximatesByName(t *testing.T) {
	src := `package p

type I interface{ Do() int }

type A struct{}

func (A) Do() int { return 1 }

func use(i I) int { return i.Do() }
`
	tests := `package p

import "testing"

func Test_Use(t *testing.T) { _ = use(nil) }
`
	scan, _ := scanPackage(covscanWrite(t, map[string]string{"a.go": src, "a_test.go": tests}), nil)
	if got := scan.candidateTests([]string{"A.Do"}); !slices.Equal(got, []string{"Test_Use"}) {
		t.Fatalf("method reached through an interface by name: %v", got)
	}
}

func TestScanPackage_HashChangesOnlyForTheEditedFunction(t *testing.T) {
	a, _ := scanPackage(covscanWrite(t, map[string]string{"lib.go": covscanLib}), nil)
	edited := `package p

type T struct{}

func (T) Method() int { return helper() }

func helper() int { return leaf() + 1 }

func leaf() int { return 1 }

func Unrelated() int { return 2 }

var table = map[string]func() int{"a": Unrelated}

var viaTable = table["a"]
`
	b, _ := scanPackage(covscanWrite(t, map[string]string{"lib.go": edited}), nil)
	for key, fa := range a.Funcs {
		changed := fa.Hash != b.Funcs[key].Hash
		if changed != (key == "helper") {
			t.Fatalf("%s changed=%v", key, changed)
		}
	}
	if a.Rest != b.Rest {
		t.Fatal("a function edit must not change the non-function hash")
	}
}

func TestScanPackage_NonFunctionDeclChangesRest(t *testing.T) {
	a, _ := scanPackage(covscanWrite(t, map[string]string{"lib.go": covscanLib}), nil)
	b, _ := scanPackage(covscanWrite(t, map[string]string{"lib.go": covscanLib + "\nvar extra = 1\n"}), nil)
	if a.Rest == b.Rest {
		t.Fatal("a new package-level declaration must change the non-function hash")
	}
}

func TestScanPackage_LineMovementKeepsFunctionHash(t *testing.T) {
	a, _ := scanPackage(covscanWrite(t, map[string]string{"lib.go": covscanLib}), nil)
	b, _ := scanPackage(covscanWrite(t, map[string]string{"lib.go": "package p\n\n// a comment\n\n" + covscanLib[len("package p\n"):]}), nil)
	if a.Funcs["leaf"].Hash != b.Funcs["leaf"].Hash {
		t.Fatal("moving a function must keep its hash")
	}
	if a.Funcs["leaf"].Start == b.Funcs["leaf"].Start {
		t.Fatal("moving a function must change its line")
	}
}

func TestScanPackage_FuncAtAndGlobals(t *testing.T) {
	scan, _ := scanPackage(covscanWrite(t, map[string]string{"lib.go": covscanLib, "lib_test.go": covscanTests}), nil)
	if got := scan.funcAt("lib.go", 9); got != "leaf" {
		t.Fatalf("funcAt = %q", got)
	}
	if !scan.Funcs["TestMain"].Global {
		t.Fatal("TestMain is global")
	}
	if got := scan.testNameList(); len(got) != 6 || got[0] != "Test_Direct" {
		t.Fatalf("test names = %v", got)
	}
}

func TestScanPackage_UnparsableSourceIsAnError(t *testing.T) {
	if _, err := scanPackage(covscanWrite(t, map[string]string{"x.go": "package p\nfunc ("}), nil); err == nil {
		t.Fatal("want an error")
	}
}
