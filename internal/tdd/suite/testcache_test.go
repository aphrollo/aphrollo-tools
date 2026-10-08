package suite

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeCacheConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte("[aphrollo]\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// The (stage, setting) table: only the post-edit suite and the commit's
// mechanical stage may serve a package from go's test cache, and only at the
// setting that names them. The merge never does, whatever the setting says.
func TestWithTestCache_StageBySettingTable(t *testing.T) {
	cases := []struct {
		stage, setting string
		want           bool
	}{
		{"edit", "", false}, {"edit", "off", false}, {"edit", "edit", true}, {"edit", "commit", true},
		{"commit", "", false}, {"commit", "off", false}, {"commit", "edit", false}, {"commit", "commit", true},
		{"merge", "", false}, {"merge", "off", false}, {"merge", "edit", false}, {"merge", "commit", false},
		{"edit", "bogus", false}, {"commit", "bogus", false},
	}
	for _, c := range cases {
		body := ""
		if c.setting != "" {
			body = fmt.Sprintf("test-cache = %q\n", c.setting)
		}
		root := writeCacheConfig(t, body)
		got := withTestCache(Runner{Cmd: "go", Args: []string{"test", "./a"}}, root, c.stage)
		if got.Cached != c.want {
			t.Errorf("stage %q, test-cache %q: Cached = %v, want %v", c.stage, c.setting, got.Cached, c.want)
		}
		if !slices.Equal(got.Args, []string{"test", "./a"}) {
			t.Errorf("stage %q, test-cache %q: Args = %v, the argv must not change here", c.stage, c.setting, got.Args)
		}
	}
}

// Only a `go test` runner is ever marked: a cargo or npm runner has no go
// cache to serve it.
func TestWithTestCache_LeavesANonGoRunnerAlone(t *testing.T) {
	root := writeCacheConfig(t, "test-cache = \"commit\"\n")
	for _, r := range []Runner{{Cmd: "cargo", Args: []string{"test"}}, {Cmd: "go", Args: []string{"vet", "./..."}}} {
		if got := withTestCache(r, root, "commit"); got.Cached {
			t.Errorf("%s %v was marked cacheable", r.Cmd, r.Args)
		}
	}
}

func TestWithTestCache_CarriesTheImpureList(t *testing.T) {
	root := writeCacheConfig(t, "test-cache = \"edit\"\ntest-cache-impure = [\"./internal/git/...\", \"./cmd/x\"]\n")
	got := withTestCache(Runner{Cmd: "go", Args: []string{"test", "./a"}}, root, "edit")
	if want := []string{"./cmd/x", "./internal/git/..."}; !slices.Equal(got.Impure, want) {
		t.Fatalf("Impure = %v, want %v", got.Impure, want)
	}
}

// With the cache off the argv is what it always was, byte for byte.
func TestGoExecArgsFor_DropsCountOnlyForACachedRunner(t *testing.T) {
	plain := Runner{Cmd: "go", Args: []string{"test", "./a"}}
	if got, want := goExecArgsFor(plain), []string{"test", "-count=1", "-json", "./a"}; !slices.Equal(got, want) {
		t.Fatalf("uncached = %v, want %v", got, want)
	}
	plain.Cached = true
	if got, want := goExecArgsFor(plain), []string{"test", "-json", "./a"}; !slices.Equal(got, want) {
		t.Fatalf("cached = %v, want %v", got, want)
	}
}

func TestSplitImpureRuns_ByTheListedPackagePatterns(t *testing.T) {
	impure := []string{"./internal/git/...", "./cmd/x"}
	run := func(pkgs ...string) Runner {
		return Runner{Cmd: "go", Args: append([]string{"test", "-shuffle=on"}, pkgs...), Cached: true, Impure: impure}
	}
	cases := []struct {
		name string
		in   Runner
		want []Runner
	}{
		{"no impure list is one cached run", Runner{Cmd: "go", Args: []string{"test", "./a"}, Cached: true},
			[]Runner{{Cmd: "go", Args: []string{"test", "./a"}, Cached: true}}},
		{"nothing impure in the list stays one cached run", run("./a", "./b"), []Runner{run("./a", "./b")}},
		{"an impure package runs apart with the count",
			run("./a", "./internal/git", "./internal/git/sub", "./cmd/x", "./b"),
			[]Runner{
				{Cmd: "go", Args: []string{"test", "-shuffle=on", "./a", "./b"}, Cached: true, Impure: impure},
				{Cmd: "go", Args: []string{"test", "-shuffle=on", "./internal/git", "./internal/git/sub", "./cmd/x"}},
			}},
		{"only impure packages is one uncached run", run("./internal/git"),
			[]Runner{{Cmd: "go", Args: []string{"test", "-shuffle=on", "./internal/git"}}}},
		{"the whole module cannot be split, so it runs uncached", run("./..."),
			[]Runner{{Cmd: "go", Args: []string{"test", "-shuffle=on", "./..."}}}},
		{"a prefix of an impure name is not it", run("./internal/gitx"), []Runner{run("./internal/gitx")}},
	}
	for _, c := range cases {
		got := splitImpureRuns(c.in)
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// The recorded capture is one real `go test -json` run of three packages, two
// of them served from the cache.
func TestCachedGoPackages_CountsThePackagesTheCacheServedInARecordedRun(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "go_test_json_cached.json"))
	if err != nil {
		t.Fatal(err)
	}
	human, _, ok := renderGoTestJSON(string(raw))
	if !ok {
		t.Fatal("the capture does not parse")
	}
	if got := cachedGoPackages(human); got != 2 {
		t.Fatalf("cachedGoPackages = %d, want 2", got)
	}
	if !strings.Contains(human, "(cached)") {
		t.Fatal("the (cached) lines must stay in the rendered output")
	}
	if got := cachedGoPackages("ok  \texample.com/x\t0.150s\n"); got != 0 {
		t.Fatalf("a fresh package counted as cached: %d", got)
	}
}

// skipUnlessGo skips the calling test when the go tool is unavailable: a
// real run needs the real tool, and a fake would prove nothing about its cache.
func skipUnlessGo(env *testing.T) {
	env.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		env.Skip("go is not installed")
	}
}

func e2eModule(t *testing.T, pkgs ...string) string {
	t.Helper()
	skipUnlessGo(t)
	root := t.TempDir()
	// a number the test logs makes this run's test binaries unseen by go's
	// cache (an unused constant compiles to the same binary)
	salt := time.Now().UnixNano()
	files := map[string]string{"go.mod": "module example.com/e2e\n\ngo 1.22\n"}
	for _, p := range pkgs {
		files[p+"/x_test.go"] = fmt.Sprintf("package x\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { t.Log(%d) }\n", salt)
	}
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A real module run twice through the runner with the cache on: the second
// run serves the pure package from go's cache and says so; the package the
// repo lists as impure reruns every time.
func TestRunSuite_CachedRunnerServesAPureGoPackageTwiceAndRerunsAnImpureOne(t *testing.T) {
	root := e2eModule(t, "pure", "impure")
	r := Runner{Cmd: "go", Args: []string{"test", "./pure", "./impure"}, Cached: true, Impure: []string{"./impure"}}
	run := RunSuite(5 * time.Minute)

	first := run(r, root)
	if !first.Passed {
		t.Fatalf("first run failed: %s", first.Output)
	}
	if got := cachedGoPackages(first.Output); got != 0 {
		t.Fatalf("first run reported %d cached packages, want 0:\n%s", got, first.Output)
	}
	second := run(r, root)
	if !second.Passed {
		t.Fatalf("second run failed: %s", second.Output)
	}
	if got := cachedGoPackages(second.Output); got != 1 {
		t.Fatalf("second run reported %d cached packages, want 1:\n%s", got, second.Output)
	}
	if !strings.Contains(second.Output, "example.com/e2e/pure\t(cached)") {
		t.Fatalf("the pure package was not the cached one:\n%s", second.Output)
	}
	if strings.Contains(second.Output, "example.com/e2e/impure\t(cached)") {
		t.Fatalf("the impure package was served from the cache:\n%s", second.Output)
	}
}

// Without the mark the gate's count=1 stands: nothing is ever served cached.
func TestRunSuite_UncachedRunnerNeverServesFromTheCache(t *testing.T) {
	root := e2eModule(t, "pure")
	r := Runner{Cmd: "go", Args: []string{"test", "./pure"}}
	run := RunSuite(5 * time.Minute)
	run(r, root)
	if second := run(r, root); cachedGoPackages(second.Output) != 0 {
		t.Fatalf("an unmarked runner was served from the cache:\n%s", second.Output)
	}
}

// The event of a settled run carries how many packages go served from its
// cache, so the measures can tell a cached green from a fresh one. A run with
// none cached adds nothing to the event.
func TestLogSuiteVerdict_RecordsTheCachedPackagesOnTheEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()

	logSuiteVerdict("postedit", root, "go test ./a ./b", "green", SuiteResult{Passed: true, Output: "ok  \texample.com/a\t(cached)\nok  \texample.com/b\t(cached)\n"})
	logSuiteVerdict("postedit", root, "go test ./c", "green", SuiteResult{Passed: true, Output: "ok  \texample.com/c\t0.1s\n"})

	var got []string
	for _, e := range ReadEvents(root) {
		if e.Stage == "postedit" {
			got = append(got, e.Cmd+"="+e.Detail["cached_packages"])
		}
	}
	if want := []string{"go test ./a ./b=2", "go test ./c="}; !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

// An entry that is not a "./dir" or "./dir/..." package pattern would match
// nothing and leave its package cached, so it is refused: the cache stays off
// for the runner and the line says which entry and what form is expected.
func TestWithTestCache_RefusesAnImpureEntryNotShapedLikeAPackagePattern(t *testing.T) {
	for _, bad := range []string{"internal/git/...", "...", "example.com/m/internal/git", "./internal/*", "./a/.../b"} {
		var warned []string
		undo := setTestCacheWarn(func(s string) { warned = append(warned, s) })
		root := writeCacheConfig(t, fmt.Sprintf("test-cache = \"edit\"\ntest-cache-impure = [\"./ok\", %q]\n", bad))
		got := withTestCache(Runner{Cmd: "go", Args: []string{"test", "./a"}}, root, "edit")
		undo()
		if got.Cached {
			t.Errorf("entry %q: the run was left cacheable", bad)
		}
		if len(warned) != 1 || !strings.Contains(warned[0], fmt.Sprintf("%q", bad)) || !strings.Contains(warned[0], "./internal/git/...") {
			t.Errorf("entry %q: warning = %q, want one naming the entry and the form ./dir or ./dir/...", bad, warned)
		}
	}
	for _, ok := range []string{"./a", "./a/b/...", "./...", "."} {
		root := writeCacheConfig(t, fmt.Sprintf("test-cache = \"edit\"\ntest-cache-impure = [%q]\n", ok))
		if got := withTestCache(Runner{Cmd: "go", Args: []string{"test", "./a"}}, root, "edit"); !got.Cached {
			t.Errorf("entry %q was refused", ok)
		}
	}
}

// A green that go's cache helped to would otherwise satisfy a later lookup of
// the same argv that wants a measured run. The key tells them apart, and an
// uncached runner keeps the key it always had.
func TestMechKey_SeparatesACachedRunFromAnUncachedOne(t *testing.T) {
	root := t.TempDir()
	plain := Runner{Cmd: "go", Args: []string{"test", "./a"}}
	cached := plain
	cached.Cached = true
	if mechKey(root, "h", plain) == mechKey(root, "h", cached) {
		t.Fatal("a cached run and an uncached one share a mech-cache key")
	}
	if got, want := mechKey(root, "h", plain), mechKeyPrefix(root, "h")+"go test ./a"; got != want {
		t.Fatalf("uncached key = %q, want %q", got, want)
	}
}
