package suite

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// test-select is per repo and defaults to off; only "edit" narrows, and a
// value nobody defined narrows nothing (the mode is read like test-cache).
func TestTestSelectMode_OnlyEditNarrows(t *testing.T) {
	for _, c := range []struct{ setting, want string }{
		{"", "off"}, {"off", "off"}, {"edit", "edit"}, {" Edit ", "edit"}, {"commit", "off"}, {"bogus", "off"},
	} {
		body := ""
		if c.setting != "" {
			body = fmt.Sprintf("test-select = %q\n", c.setting)
		}
		if got := editSelectMode(writeCacheConfig(t, body)); got != c.want {
			t.Errorf("test-select %q: mode = %q, want %q", c.setting, got, c.want)
		}
	}
}

func TestWithSelectedTests_AnchorsTheRunPatternAndKeepsThePackage(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./internal/p"}}
	tests := []string{"TestB", "TestA"}
	got := withSelectedTests(base, tests, 9, []string{"F"})
	if !slices.Equal(tests, []string{"TestB", "TestA"}) {
		t.Fatalf("the caller's tests were reordered: %v", tests)
	}
	want := []string{"test", "./internal/p", "-run=^(TestA|TestB)$"}
	if !slices.Equal(got.Args, want) {
		t.Fatalf("args = %q, want %q", got.Args, want)
	}
	if got.Select == nil || got.Select.Run != 2 || got.Select.Total != 9 || !slices.Equal(got.Select.Funcs, []string{"F"}) || got.Select.Reason != "" {
		t.Fatalf("select = %+v", got.Select)
	}
	if !slices.Equal(base.Args, []string{"test", "./internal/p"}) {
		t.Fatalf("the input runner's argv was changed: %v", base.Args)
	}
}

// A test whose name is the prefix of another must not select the other, and
// the same names in any order, with repeats, make the same bytes.
func TestWithSelectedTests_APrefixNameSelectsOnlyItselfAndOrderDoesNotMatter(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./p"}}
	one := withSelectedTests(base, []string{"TestFoo"}, 3, []string{"F"})
	if got, want := one.Args[2], "-run=^(TestFoo)$"; got != want {
		t.Fatalf("run arg = %q, want %q", got, want)
	}
	a := withSelectedTests(base, []string{"TestFoo", "TestFooBar", "TestFoo"}, 5, []string{"F"})
	b := withSelectedTests(base, []string{"TestFooBar", "TestFoo"}, 5, []string{"F"})
	if !slices.Equal(a.Args, b.Args) || a.Args[2] != "-run=^(TestFoo|TestFooBar)$" {
		t.Fatalf("a = %q, b = %q", a.Args, b.Args)
	}
}

func TestWithSelectedTests_ReadsBackAsOnePackage(t *testing.T) {
	got := withSelectedTests(Runner{Cmd: "go", Args: []string{"test", "./p"}}, []string{"TestA"}, 2, []string{"F"})
	pkgs, _ := argvbatch.GoTestPackages(got.Cmd, got.Args)
	if !slices.Equal(pkgs, []string{"./p"}) {
		t.Fatalf("packages = %v, want [./p]", pkgs)
	}
}

func TestWithSelectedTests_RunsTheWholePackageWhenItCannotNarrowSafely(t *testing.T) {
	long := make([]string, 0, 900)
	for i := range 900 {
		long = append(long, fmt.Sprintf("TestGenerated%04d", i))
	}
	cases := []struct {
		name   string
		r      Runner
		tests  []string
		total  int
		reason string
	}{
		{"several packages", Runner{Cmd: "go", Args: []string{"test", "./a", "./b"}}, []string{"TestA"}, 4, "one go package"},
		{"the whole module", Runner{Cmd: "go", Args: []string{"test", "./..."}}, []string{"TestA"}, 4, "one go package"},
		{"not go test", Runner{Cmd: "cargo", Args: []string{"test"}}, []string{"TestA"}, 4, "one go package"},
		{"every test selected", Runner{Cmd: "go", Args: []string{"test", "./a"}}, []string{"TestA", "TestB"}, 2, "whole package"},
		{"nothing selected", Runner{Cmd: "go", Args: []string{"test", "./a"}}, nil, 2, "no test"},
		{"a pattern too long for a command line", Runner{Cmd: "go", Args: []string{"test", "./a"}}, long, 5000, "too long"},
	}
	for _, c := range cases {
		got := withSelectedTests(c.r, c.tests, c.total, []string{"F"})
		if !slices.Equal(got.Args, c.r.Args) {
			t.Errorf("%s: args = %v, want them unchanged", c.name, got.Args)
		}
		if got.Select == nil || !strings.Contains(got.Select.Reason, c.reason) || got.Select.Run != 0 {
			t.Errorf("%s: select = %+v, want a reason with %q", c.name, got.Select, c.reason)
		}
	}
}

// A run narrowed to some tests proves those tests alone: its green must not
// answer for the package's whole suite at the commit gate.
func TestWithSelectedTests_ASelectedGreenDoesNotCoverTheWholePackage(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := writeCacheConfig(t, "")
	full := Runner{Cmd: "go", Args: []string{"test", "./p"}}
	selected := withSelectedTests(full, []string{"TestA"}, 3, []string{"F"})
	mechCacheAddUnmoved(root, "", selected)
	if mechCacheCovers(root, "", full) {
		t.Fatal("a stateless lookup was answered")
	}
	mechCacheAdd(mechKey(root, "state-1", selected))
	if mechCacheCovers(root, "state-1", full) {
		t.Fatal("a green of the selected tests answered for the whole package")
	}
	if !mechCacheCovers(root, "state-1", selected) {
		t.Fatal("the selected run's own green is not found: the premise of this test is broken")
	}
}

// A name is quoted for the regexp, so nothing in it can widen the selection.
func TestWithSelectedTests_QuotesEachNameForTheRegexp(t *testing.T) {
	got := withSelectedTests(Runner{Cmd: "go", Args: []string{"test", "./p"}}, []string{"Test.Odd", "Test|Pipe"}, 5, []string{"F"})
	if want := `-run=^(Test\.Odd|Test\|Pipe)$`; got.Args[2] != want {
		t.Fatalf("run arg = %q, want %q", got.Args[2], want)
	}
}

// The event of a settled edit run says how many tests were selected of how
// many, beside the cached packages, so the measures can tell a selected green
// from a full one (a run that selected nothing adds nothing).
func TestLogSuiteVerdictWith_RecordsTheSelectionOnTheEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()

	logSuiteVerdictWith("postedit", root, "go test ./a -run=^(TestA)$", "green", SuiteResult{Passed: true, Output: "ok  \texample.com/a\t(cached)\n"}, map[string]string{"selected": "1", "total": "7"})
	logSuiteVerdict("postedit", root, "go test ./c", "green", SuiteResult{Passed: true, Output: "ok  \texample.com/c\t0.1s\n"})

	var got []string
	for _, e := range ReadEvents(root) {
		if e.Stage == "postedit" {
			got = append(got, e.Cmd+"="+e.Detail["selected"]+"/"+e.Detail["total"]+"/"+e.Detail["cached_packages"])
		}
	}
	if want := []string{"go test ./a -run=^(TestA)$=1/7/1", "go test ./c=//"}; !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

// A pattern of exactly selectArgMax characters is still one argument; one more
// is refused and the package runs whole.
func TestWithSelectedTests_ThePatternLengthLimitIsInclusive(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./p"}}
	atLimit := withSelectedTests(base, []string{"Test" + strings.Repeat("a", selectArgMax-4-len("Test"))}, 9, []string{"F"})
	if atLimit.Select == nil || atLimit.Select.Reason != "" || len(atLimit.Args) != 3 {
		t.Fatalf("a pattern of %d characters: select = %+v", selectArgMax, atLimit.Select)
	}
	over := withSelectedTests(base, []string{"Test" + strings.Repeat("a", selectArgMax-3-len("Test"))}, 9, []string{"F"})
	if over.Select == nil || !strings.Contains(over.Select.Reason, "too long") || len(over.Args) != 2 {
		t.Fatalf("a pattern of %d characters: select = %+v", selectArgMax+1, over.Select)
	}
}
