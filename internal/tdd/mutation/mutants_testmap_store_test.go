package mutation

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// covermapRepo is a git repository whose shared git directory holds the stores.
func covermapRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return makeGoRepo(t)
}

// sampleStore is a store of one package holding one measured test.
func sampleStore() *covStore {
	return &covStore{
		Schema: covSchema, Package: "internal/p", Env: "e1e1e1e1e1e1e1e1e1e1",
		Rest:   "rest",
		Shapes: map[string]covShape{"f": {Hash: "hf", Blocks: [][2]int{{1, 1}}}},
		Tests:  map[string]covTest{"TestA": {Hash: "ha", Cover: []covFunc{{Func: "f", Hash: "hf", Blocks: [][2]int{{1, 1}}}}}},
	}
}

func TestCovStore_RoundTrip(t *testing.T) {
	root := covermapRepo(t)
	want := sampleStore()
	if err := want.save(root); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := loadCovStore(root, "internal/p", want.Env)
	if got.Rest != "rest" || got.Tests["TestA"].Hash != "ha" || got.Tests["TestA"].Cover[0].Blocks[0] != [2]int{1, 1} || got.Shapes["f"].Hash != "hf" {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
	if other := loadCovStore(root, "internal/other", want.Env); len(other.Tests) != 0 {
		t.Error("a store was found for a package none was saved for")
	}
}

// A store is the store of one build key and no other: another key, package or
// schema inside the file is not trusted, whatever the file's name says.
func TestCovStore_AStoreOfAnotherKeyIsNeverUsed(t *testing.T) {
	root := covermapRepo(t)
	st := sampleStore()
	if err := st.save(root); err != nil {
		t.Fatal(err)
	}
	if got := loadCovStore(root, "internal/p", "0000000000000000000"); len(got.Tests) != 0 {
		t.Error("a store kept for another key was used")
	}
	// The same file name with another key inside it: the name carries only the
	// first 16 characters of the key.
	other := *st
	other.Env = st.Env[:16] + "ffff"
	if err := other.save(root); err != nil {
		t.Fatal(err)
	}
	if got := loadCovStore(root, "internal/p", st.Env); len(got.Tests) != 0 {
		t.Error("a file whose own key differs from the key asked for was used")
	}
}

func TestCovStore_UntrustedStoresAreNoStore(t *testing.T) {
	root := covermapRepo(t)
	st := sampleStore()
	st.Schema = covSchema + 1
	if err := st.save(root); err != nil {
		t.Fatal(err)
	}
	if got := loadCovStore(root, "internal/p", st.Env); len(got.Tests) != 0 {
		t.Error("a store of another schema was trusted")
	}
	st = sampleStore()
	if err := os.MkdirAll(filepath.Dir(covStorePath(root, st.Package, st.Env)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(covStorePath(root, st.Package, st.Env), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadCovStore(root, "internal/p", st.Env); len(got.Tests) != 0 || got.Schema != covSchema || got.Package != "internal/p" {
		t.Errorf("a file that is not JSON gave %+v, want an empty store of this package", got)
	}
}

// The stores live in the repository's shared git directory: every worktree of
// one repository reads what any of them measured, and another repository's
// stores are elsewhere.
func TestCovStorePath_AllWorktreesOfARepoShareTheStores(t *testing.T) {
	primary := covermapRepo(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, primary, "worktree", "add", "-q", "-b", "lane/x", lane)
	a, b := covStorePath(primary, "internal/p", "h1h1h1h1h1h1h1h1"), covStorePath(lane, "internal/p", "h1h1h1h1h1h1h1h1")
	if a == "" || a != b {
		t.Errorf("the primary's store is %q and the lane's is %q, want one file", a, b)
	}
	if !strings.HasPrefix(filepath.ToSlash(a), filepath.ToSlash(covermapGitPath(t, primary, "rev-parse", "--git-common-dir"))) {
		t.Errorf("store %q is not under the shared git directory", a)
	}
	other := makeGoRepo(t)
	if c := covStorePath(other, "internal/p", "h1h1h1h1h1h1h1h1"); a == c {
		t.Errorf("two repositories share the store file %s", a)
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

func TestCovStorePath_OnePerPackageAndKey(t *testing.T) {
	root := covermapRepo(t)
	a, b, top := covStorePath(root, "internal/a", "h1h1h1h1h1h1h1h1"), covStorePath(root, "internal/b", "h1h1h1h1h1h1h1h1"), covStorePath(root, ".", "h1h1h1h1h1h1h1h1")
	if a == b || a == top || b == top {
		t.Errorf("paths collide: %s %s %s", a, b, top)
	}
	if c := covStorePath(root, "internal/a", "h2h2h2h2h2h2h2h2"); c == a {
		t.Errorf("two keys of one package share the file %s", a)
	}
	if filepath.Dir(a) != filepath.Dir(b) {
		t.Errorf("stores of one repo live in different directories: %s vs %s", a, b)
	}
}

// A directory outside any git repository has no shared git directory to keep
// stores in, so nothing is kept and saving says so.
func TestCovStore_NoRepositoryKeepsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	if p := covStorePath(root, "internal/p", "h1"); p != "" {
		t.Errorf("path = %q, want none outside a repository", p)
	}
	if err := sampleStore().save(root); err == nil {
		t.Error("saving outside a repository succeeded")
	}
	if got := loadCovStore(root, "internal/p", "h1"); len(got.Tests) != 0 {
		t.Error("a store was loaded outside a repository")
	}
}

// The directory is bounded: past the entry limit, the stores read longest ago go,
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
		t.Errorf("left %v, want the two newest stores and the file that is no store", left)
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

// A store that is read counts as used, so what is trimmed is what nothing reads.
func TestLoadCovStore_MarksTheStoreRead(t *testing.T) {
	root := covermapRepo(t)
	st := sampleStore()
	if err := st.save(root); err != nil {
		t.Fatal(err)
	}
	path := covStorePath(root, "internal/p", st.Env)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := loadCovStore(root, "internal/p", st.Env); len(got.Tests) != 1 {
		t.Fatal("no store")
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > time.Hour {
		t.Errorf("modified %v (%v) after a read, want it marked as just used", info.ModTime(), err)
	}
}

// ratchet: test_removed TestHashPackage_FollowsTheListedSources: the store keys no package content; function hashes say what changed (TestScanPackage_HashChangesOnlyForTheEditedFunction)
// ratchet: test_removed TestHashPackage_ListingOrderDoesNotMatter: there is no listing to order
// ratchet: test_removed TestHashPackage_ExternalDependenciesAreNamedNotRead: imported packages are deliberately not in the key, as the store's header says
// ratchet: test_removed TestHashPackage_AnUnreadableFileChangesTheHash: an unreadable source is an error in scanPackage
// ratchet: test_removed TestHashPackage_EmptyListing: there is no listing
// ratchet: test_removed TestTestMapStore_RoundTrip: TestCovStore_RoundTrip
// ratchet: test_removed TestTestMapStore_AMapOfOtherContentIsNeverUsed: TestCovStore_AStoreOfAnotherKeyIsNeverUsed
// ratchet: test_removed TestTestMapStore_TwoContentsOfOnePackageAreKeptSideBySide: the store is per package and build key and changes in place; edits are proved in the plan tests
// ratchet: test_removed TestTestMapStore_UntrustedMapsAreNoMap: TestCovStore_UntrustedStoresAreNoStore
// ratchet: test_removed TestTestMapPath_AllWorktreesOfARepoShareTheMaps: TestCovStorePath_AllWorktreesOfARepoShareTheStores
// ratchet: test_removed TestTestMapPath_OnePerPackageAndContent: TestCovStorePath_OnePerPackageAndKey
// ratchet: test_removed TestTestMapStore_NoRepositoryKeepsNothing: TestCovStore_NoRepositoryKeepsNothing
// ratchet: test_removed TestLoadTestMap_MarksTheMapRead: TestLoadCovStore_MarksTheStoreRead
