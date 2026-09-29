package suite

import (
	"reflect"
	"testing"
)

// These are suite's own tests of mutants_deps.go: goWorkspaceDeps and
// goPackageDirs. Both shell out to `go list` against a real module, so the
// tests build a small one on disk.

// depsModule lays down module example.com/m: the root package imports a, a
// imports b, and c stands alone. Only packages inside the module are edges.
func depsModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "m.go", "package m\n\nimport _ \"example.com/m/a\"\n")
	write(t, root, "a/a.go", "package a\n\nimport (\n\t_ \"example.com/m/b\"\n\t_ \"strings\"\n)\n")
	write(t, root, "b/b.go", "package b\n")
	write(t, root, "c/c.go", "package c\n")
	return root
}

// Serial: goPackageDirs lists the process's working directory, which the test changes.
// TestGoWorkspaceDeps_KeepsOnlyEdgesInsideTheModuleKeyedByRelativeDir pins the
// answer: each package directory, relative to the repo root with "" for the
// root's own package, maps to the sorted module packages it depends on, and
// the standard library and self-edges are left out.
func TestGoWorkspaceDeps_KeepsOnlyEdgesInsideTheModuleKeyedByRelativeDir(t *testing.T) {
	root := depsModule(t)
	t.Chdir(root)

	got, err := goWorkspaceDeps(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"": {"a", "b"}, "a": {"b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("goWorkspaceDeps = %v, want %v", got, want)
	}
}

// Serial: goPackageDirs lists the process's working directory, which the test changes.
// TestGoWorkspaceDeps_APackageWithNoModuleDependenciesIsAbsent pins that a
// leaf (b, c) has no entry at all rather than an empty one.
func TestGoWorkspaceDeps_APackageWithNoModuleDependenciesIsAbsent(t *testing.T) {
	root := depsModule(t)
	t.Chdir(root)

	got, err := goWorkspaceDeps(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaf := range []string{"b", "c"} {
		if _, has := got[leaf]; has {
			t.Fatalf("goWorkspaceDeps has an entry for leaf %q: %v", leaf, got)
		}
	}
}

// TestGoWorkspaceDeps_ARootThatIsNoModuleIsAnError pins the failure: go list
// cannot answer, and the error comes back rather than an empty graph that
// would read as "nothing depends on anything".
func TestGoWorkspaceDeps_ARootThatIsNoModuleIsAnError(t *testing.T) {
	t.Parallel()
	if _, err := goWorkspaceDeps(t.TempDir()); err == nil {
		t.Fatal("goWorkspaceDeps over a directory with no module returned no error")
	}
}

// Serial: goPackageDirs lists the process's working directory, which the test changes.
// TestGoPackageDirs_MapsEveryImportPathToItsRelativeDirectory pins the map.
func TestGoPackageDirs_MapsEveryImportPathToItsRelativeDirectory(t *testing.T) {
	root := depsModule(t)
	t.Chdir(root)

	got, err := goPackageDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"example.com/m":   "",
		"example.com/m/a": "a",
		"example.com/m/b": "b",
		"example.com/m/c": "c",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("goPackageDirs = %v, want %v", got, want)
	}
}

// Serial: goPackageDirs lists the process's working directory, which the test changes.
// TestGoPackageDirs_OutsideAModuleIsAnError pins the failure arm.
func TestGoPackageDirs_OutsideAModuleIsAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := goPackageDirs("/anywhere"); err == nil {
		t.Fatal("goPackageDirs outside a module returned no error")
	}
}

// TestRelPackageDir_IsForwardSlashedAndEmptyForTheRoot pins the keying.
func TestRelPackageDir_IsForwardSlashedAndEmptyForTheRoot(t *testing.T) {
	t.Parallel()
	if got := relPackageDir("/repo", "/repo"); got != "" {
		t.Fatalf("relPackageDir(root, root) = %q, want empty", got)
	}
	if got := relPackageDir("/repo", "/repo/internal/x"); got != "internal/x" {
		t.Fatalf("relPackageDir = %q, want internal/x", got)
	}
}

// TestRelPackageDir_AnUnrelatablePathFallsBackToItself pins the fallback: a
// relative root against an absolute dir cannot be related, and the dir comes
// back slash-normalised.
func TestRelPackageDir_AnUnrelatablePathFallsBackToItself(t *testing.T) {
	t.Parallel()
	if got := relPackageDir("rel", "/abs/dir"); got != "/abs/dir" {
		t.Fatalf("relPackageDir = %q, want /abs/dir", got)
	}
}
