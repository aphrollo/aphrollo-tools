package mutation

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// hashFixture is a module root with one package, a dependency and the `go
// list` listing that names them.
func hashFixture(t *testing.T) (root, listing string) {
	t.Helper()
	root = t.TempDir()
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "a", "a_test.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "a", "unlisted.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "dep", "dep.go"), "package dep\n")
	listing = filepath.Join(root, "a") + "|a.go|a_test.go||\n" +
		"\n" +
		filepath.Join(root, "dep") + "|dep.go|||\n"
	return root, listing
}

func TestHashPackage_FollowsTheListedSources(t *testing.T) {
	t.Parallel()
	root, listing := hashFixture(t)
	base := hashPackage(root, listing)
	if base == "" {
		t.Fatal("hash is empty")
	}
	if again := hashPackage(root, listing); again != base {
		t.Errorf("same tree hashed to %s then %s", base, again)
	}
	for _, tc := range []struct {
		name    string
		rewrite string
		want    bool // true: the hash changes
	}{
		{"the package's own source", filepath.Join("a", "a.go"), true},
		{"the package's own test", filepath.Join("a", "a_test.go"), true},
		{"a dependency's source", filepath.Join("dep", "dep.go"), true},
		{"a file no listing names", filepath.Join("a", "unlisted.go"), false},
	} {
		root, listing := hashFixture(t)
		before := hashPackage(root, listing)
		mustWrite(t, filepath.Join(root, tc.rewrite), "package changed\n")
		if changed := hashPackage(root, listing) != before; changed != tc.want {
			t.Errorf("%s: hash changed = %v, want %v", tc.name, changed, tc.want)
		}
	}
}

func TestHashPackage_ListingOrderDoesNotMatter(t *testing.T) {
	t.Parallel()
	root, listing := hashFixture(t)
	lines := []string{filepath.Join(root, "dep") + "|dep.go|||", filepath.Join(root, "a") + "|a.go|a_test.go||", ""}
	reordered := lines[0] + "\n" + lines[2] + "\n" + lines[1] + "\n"
	if hashPackage(root, listing) != hashPackage(root, reordered) {
		t.Error("the hash depends on the order the listing names packages in")
	}
}

// A dependency outside the module is named by its directory, which carries the
// version, and its files are not read.
func TestHashPackage_ExternalDependenciesAreNamedNotRead(t *testing.T) {
	t.Parallel()
	root, listing := hashFixture(t)
	external := listing + "/elsewhere/mod@v1.0.0|x.go|||\n"
	if hashPackage(root, external) == hashPackage(root, listing) {
		t.Error("adding an external dependency left the hash unchanged")
	}
	bumped := listing + "/elsewhere/mod@v1.0.1|x.go|||\n"
	if hashPackage(root, external) == hashPackage(root, bumped) {
		t.Error("a new dependency version left the hash unchanged")
	}
}

// A listed file that cannot be read is not the same tree as one that can.
func TestHashPackage_AnUnreadableFileChangesTheHash(t *testing.T) {
	t.Parallel()
	root, listing := hashFixture(t)
	present := hashPackage(root, listing)
	if err := os.Remove(filepath.Join(root, "a", "a.go")); err != nil {
		t.Fatal(err)
	}
	if hashPackage(root, listing) == present {
		t.Error("a deleted source left the hash unchanged")
	}
}

func TestHashPackage_EmptyListing(t *testing.T) {
	t.Parallel()
	if got := hashPackage(t.TempDir(), ""); got == "" {
		t.Error("an empty listing must still hash to a value")
	}
}

func sampleMap() testMap {
	return testMap{
		Schema: testMapSchema, Package: "internal/p", Hash: "h1",
		Tests: []string{"TestA", "TestB"}, Funcs: map[string][]int{"f": {0, 1}, "g": {1}},
	}
}

func TestTestMapStore_RoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	want := sampleMap()
	if err := saveTestMap(root, want); err != nil {
		t.Fatalf("saveTestMap: %v", err)
	}
	got, ok := loadTestMap(root, "internal/p")
	if !ok {
		t.Fatal("loadTestMap: no map")
	}
	if got.Hash != "h1" || !slices.Equal(got.Tests, want.Tests) || !slices.Equal(got.Funcs["f"], []int{0, 1}) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
	if _, ok := loadTestMap(root, "internal/other"); ok {
		t.Error("a map was found for a package none was saved for")
	}
}

// A map that is not one this build can trust is no map: another schema, an
// index past the test list, or a file that is not JSON.
func TestTestMapStore_UntrustedMapsAreNoMap(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	for _, tc := range []struct {
		name   string
		mutate func(*testMap)
	}{
		{"another schema", func(m *testMap) { m.Schema = testMapSchema + 1 }},
		{"an index at the end of the test list", func(m *testMap) { m.Funcs["f"] = []int{len(m.Tests)} }},
		{"a negative index", func(m *testMap) { m.Funcs["f"] = []int{-1} }},
	} {
		m := sampleMap()
		tc.mutate(&m)
		if err := saveTestMap(root, m); err != nil {
			t.Fatalf("%s: saveTestMap: %v", tc.name, err)
		}
		if _, ok := loadTestMap(root, "internal/p"); ok {
			t.Errorf("%s: the map was trusted", tc.name)
		}
	}
	// The last valid index is the one before the end of the list.
	m := sampleMap()
	m.Funcs["f"] = []int{len(m.Tests) - 1}
	if err := saveTestMap(root, m); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadTestMap(root, "internal/p"); !ok {
		t.Error("an index at the last test was refused")
	}
	if err := os.WriteFile(testMapPath(root, "internal/p"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadTestMap(root, "internal/p"); ok {
		t.Error("a file that is not JSON was trusted")
	}
}

func TestTestMapPath_OnePerPackage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	a, b, top := testMapPath(root, "internal/a"), testMapPath(root, "internal/b"), testMapPath(root, ".")
	if a == b || a == top || b == top {
		t.Errorf("paths collide: %s %s %s", a, b, top)
	}
	if filepath.Dir(a) != filepath.Dir(b) {
		t.Errorf("maps of one repo live in different directories: %s vs %s", a, b)
	}
}
