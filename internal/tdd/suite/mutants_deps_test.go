package suite

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// A throwaway module whose package a imports b, laid out under a temp dir the
// test process is not standing in.
func twoPackageModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"go.mod": "module example.test/m\n\ngo 1.21\n",
		"a/a.go": "package a\n\nimport _ \"example.test/m/b\"\n",
		"b/b.go": "package b\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Issue #991: goPackageDirs answers about the root it is given, not about the
// directory the gate process happens to be standing in (this package's own).
func TestGoPackageDirs_ListsTheGivenRootNotTheProcessCwd(t *testing.T) {
	root := twoPackageModule(t)
	got, err := goPackageDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"example.test/m/a": "a", "example.test/m/b": "b"}
	if !maps.Equal(got, want) {
		t.Fatalf("goPackageDirs(%s) = %v, want %v", root, got, want)
	}
}

func TestGoWorkspaceDeps_ReportsTheGivenRootsEdges(t *testing.T) {
	root := twoPackageModule(t)
	got, err := goWorkspaceDeps(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["a"]) != 1 || got["a"][0] != "b" {
		t.Fatalf("goWorkspaceDeps(%s) = %v, want map[a:[b]]", root, got)
	}
}
