package mutation

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	external := listing + "/elsewhere/pkg/mod/mod@v1.0.0|x.go|||\n"
	if hashPackage(root, external) == hashPackage(root, listing) {
		t.Error("adding an external dependency left the hash unchanged")
	}
	bumped := listing + "/elsewhere/pkg/mod/mod@v1.0.1|x.go|||\n"
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
		Schema: testMapSchema, Package: "internal/p", Hash: "h1h1h1h1h1h1h1h1h1",
		Tests: []string{"TestA", "TestB"}, Blocks: []mapBlock{
			{coverBlock{File: "p.go", From: 4, To: 4}, []int{0, 1}},
			{coverBlock{File: "p.go", From: 8, To: 8}, []int{1}},
		},
	}
}

// covermapRepo is a git repository whose shared git directory holds the maps.
func covermapRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return makeGoRepo(t)
}

func TestTestMapStore_RoundTrip(t *testing.T) {
	root := covermapRepo(t)
	want := sampleMap()
	if err := saveTestMap(root, want); err != nil {
		t.Fatalf("saveTestMap: %v", err)
	}
	got, ok := loadTestMap(root, "internal/p", want.Hash)
	if !ok {
		t.Fatal("loadTestMap: no map")
	}
	if got.Hash != want.Hash || !slices.Equal(got.Tests, want.Tests) ||
		got.Blocks[0].coverBlock != want.Blocks[0].coverBlock || !slices.Equal(got.Blocks[0].Tests, []int{0, 1}) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
	if _, ok := loadTestMap(root, "internal/other", want.Hash); ok {
		t.Error("a map was found for a package none was saved for")
	}
}

// A map is the map of one content and no other: a package whose files changed
// has another hash, and the map kept for the old one is never read for it.
func TestTestMapStore_AMapOfOtherContentIsNeverUsed(t *testing.T) {
	root := covermapRepo(t)
	m := sampleMap()
	if err := saveTestMap(root, m); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadTestMap(root, "internal/p", "0000000000000000000"); ok {
		t.Error("a map kept for another hash was used")
	}
	// The same file name with another hash inside it is not trusted either:
	// the name carries only the first 16 characters of the key.
	other := m
	other.Hash = m.Hash[:16] + "ffff"
	if err := os.WriteFile(testMapPath(root, "internal/p", m.Hash), covermapMustJSON(t, other), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadTestMap(root, "internal/p", m.Hash); ok {
		t.Error("a file whose own hash differs from the key asked for was used")
	}
}

func covermapMustJSON(t *testing.T, m testMap) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Two contents of one package are two files, so two lanes on different
// content do not replace each other's map.
func TestTestMapStore_TwoContentsOfOnePackageAreKeptSideBySide(t *testing.T) {
	root := covermapRepo(t)
	a, b := sampleMap(), sampleMap()
	b.Hash = "2222222222222222222"
	b.Tests = []string{"TestOnlyB"}
	b.Blocks = nil
	if err := saveTestMap(root, a); err != nil {
		t.Fatal(err)
	}
	if err := saveTestMap(root, b); err != nil {
		t.Fatal(err)
	}
	if got, ok := loadTestMap(root, "internal/p", a.Hash); !ok || len(got.Tests) != 2 {
		t.Errorf("the first content's map = %+v (kept %v), want it still there", got, ok)
	}
	if got, ok := loadTestMap(root, "internal/p", b.Hash); !ok || !slices.Equal(got.Tests, []string{"TestOnlyB"}) {
		t.Errorf("the second content's map = %+v (kept %v)", got, ok)
	}
}

// A map that is not one this build can trust is no map: another schema, an
// index past the test list, or a file that is not JSON.
func TestTestMapStore_UntrustedMapsAreNoMap(t *testing.T) {
	root := covermapRepo(t)
	for _, tc := range []struct {
		name   string
		mutate func(*testMap)
	}{
		{"another schema", func(m *testMap) { m.Schema = testMapSchema + 1 }},
		{"an index at the end of the test list", func(m *testMap) { m.Blocks[0].Tests = []int{len(m.Tests)} }},
		{"a negative index", func(m *testMap) { m.Blocks[0].Tests = []int{-1} }},
	} {
		m := sampleMap()
		tc.mutate(&m)
		if err := saveTestMap(root, m); err != nil {
			t.Fatalf("%s: saveTestMap: %v", tc.name, err)
		}
		if _, ok := loadTestMap(root, "internal/p", m.Hash); ok {
			t.Errorf("%s: the map was trusted", tc.name)
		}
	}
	// The last valid index is the one before the end of the list.
	m := sampleMap()
	m.Blocks[0].Tests = []int{len(m.Tests) - 1}
	if err := saveTestMap(root, m); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadTestMap(root, "internal/p", m.Hash); !ok {
		t.Error("an index at the last test was refused")
	}
	if err := os.WriteFile(testMapPath(root, "internal/p", m.Hash), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadTestMap(root, "internal/p", m.Hash); ok {
		t.Error("a file that is not JSON was trusted")
	}
}

// The maps live in the repository's shared git directory: every worktree of
// one repository reads what any of them measured, and another repository's
// maps are elsewhere.
func TestTestMapPath_AllWorktreesOfARepoShareTheMaps(t *testing.T) {
	primary := covermapRepo(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, primary, "worktree", "add", "-q", "-b", "lane/x", lane)
	a, b := testMapPath(primary, "internal/p", "h1h1h1h1h1h1h1h1"), testMapPath(lane, "internal/p", "h1h1h1h1h1h1h1h1")
	if a == "" || a != b {
		t.Errorf("the primary's map is %q and the lane's is %q, want one file", a, b)
	}
	if !strings.HasPrefix(filepath.ToSlash(a), filepath.ToSlash(covermapGitPath(t, primary, "rev-parse", "--git-common-dir"))) {
		t.Errorf("map %q is not under the shared git directory", a)
	}
	other := makeGoRepo(t)
	if c := testMapPath(other, "internal/p", "h1h1h1h1h1h1h1h1"); a == c {
		t.Errorf("two repositories share the map file %s", a)
	}
}

// covermapGitPath is the trimmed output of a git command in dir, as an absolute slash path.
func covermapGitPath(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output() // stderr-ok: a failure shows as an empty path, which the assertion reports
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.ToSlash(filepath.Clean(p))
}

func TestTestMapPath_OnePerPackageAndContent(t *testing.T) {
	root := covermapRepo(t)
	a, b, top := testMapPath(root, "internal/a", "h1h1h1h1h1h1h1h1"), testMapPath(root, "internal/b", "h1h1h1h1h1h1h1h1"), testMapPath(root, ".", "h1h1h1h1h1h1h1h1")
	if a == b || a == top || b == top {
		t.Errorf("paths collide: %s %s %s", a, b, top)
	}
	if c := testMapPath(root, "internal/a", "h2h2h2h2h2h2h2h2"); c == a {
		t.Errorf("two contents of one package share the file %s", a)
	}
	if filepath.Dir(a) != filepath.Dir(b) {
		t.Errorf("maps of one repo live in different directories: %s vs %s", a, b)
	}
}

// A directory outside any git repository has no shared git directory to keep
// maps in, so nothing is kept and saving says so.
func TestTestMapStore_NoRepositoryKeepsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	if p := testMapPath(root, "internal/p", "h1"); p != "" {
		t.Errorf("path = %q, want none outside a repository", p)
	}
	if err := saveTestMap(root, sampleMap()); err == nil {
		t.Error("saving outside a repository succeeded")
	}
	if _, ok := loadTestMap(root, "internal/p", "h1"); ok {
		t.Error("a map was loaded outside a repository")
	}
}

// The directory is bounded: past the entry limit, the maps read longest ago go,
// and the one just written stays.
func TestTrimCoverCache_KeepsTheNewestWithinTheEntryLimit(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, name := range []string{"a.json", "b.json", "c.json", "d.json"} {
		path := filepath.Join(dir, name)
		mustWrite(t, path, "{}")
		if err := os.Chtimes(path, now.Add(-time.Duration(4-i)*time.Hour), now.Add(-time.Duration(4-i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(dir, "notes.txt"), "not a map")

	trimCoverCache(dir, 2, 1<<20)

	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if want := []string{"c.json", "d.json", "notes.txt"}; !slices.Equal(left, want) {
		t.Errorf("left %v, want the two newest maps and the file that is no map", left)
	}
}

func TestTrimCoverCache_KeepsTheNewestWithinTheByteLimitAndAlwaysTheLatest(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, name := range []string{"old.json", "new.json"} {
		path := filepath.Join(dir, name)
		mustWrite(t, path, strings.Repeat("x", 100))
		if err := os.Chtimes(path, now.Add(-time.Duration(2-i)*time.Hour), now.Add(-time.Duration(2-i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	trimCoverCache(dir, 10, 50) // each file is over the limit alone

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "new.json" {
		t.Errorf("left %v, want only the newest, kept though it is over the limit", entries)
	}
}

// A map that is read counts as used, so what is trimmed is what nothing reads.
func TestLoadTestMap_MarksTheMapRead(t *testing.T) {
	root := covermapRepo(t)
	m := sampleMap()
	if err := saveTestMap(root, m); err != nil {
		t.Fatal(err)
	}
	path := testMapPath(root, "internal/p", m.Hash)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadTestMap(root, "internal/p", m.Hash); !ok {
		t.Fatal("no map")
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > time.Hour {
		t.Errorf("modified %v (%v) after a read, want it marked as just used", info.ModTime(), err)
	}
}

// ratchet: test_removed TestTestMapPath_OnePerPackage: the path is now per package and content; TestTestMapPath_OnePerPackageAndContent is its replacement
