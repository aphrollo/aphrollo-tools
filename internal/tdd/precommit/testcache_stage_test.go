package precommit

import (
	"reflect"
	"slices"
	"testing"
)

// The flag set a go suite carries is picked by (stage, test-cache setting).
// The merge always carries -count=1 and -shuffle=on, whatever the runner
// says; a cached run elsewhere carries neither, because -shuffle is not a
// cacheable flag and a run with it never reads go's cache.
func TestWithGoCIParity_CachedRunnerDropsCountAndShuffleExceptAtTheMerge(t *testing.T) {
	t.Parallel()
	impure := []string{"./internal/git/..."}
	cached := Runner{Cmd: "go", Args: []string{"test", "./internal/x"}, Cached: true, Impure: impure}

	got := withGoCIParity(cached, false)
	want := Runner{Cmd: "go", Args: []string{"test", "./internal/x"}, Cached: true, Impure: impure}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cached at the commit = %+v, want %+v", got, want)
	}

	got = withGoCIParity(cached, true)
	want = Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "./internal/x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cached at the merge = %+v, want %+v (the merge is never served from the cache)", got, want)
	}
}

// The runner's other fields survive the flag insertion.
func TestWithGoCIParity_KeepsTheRunnersEnv(t *testing.T) {
	t.Parallel()
	got := withGoCIParity(Runner{Cmd: "go", Args: []string{"test", "./x"}, Env: []string{"A=1"}}, false)
	if !slices.Equal(got.Env, []string{"A=1"}) {
		t.Fatalf("Env = %v, want [A=1]", got.Env)
	}
}

// groupSuiteRunner is the suite a stage owes. With test-cache = "commit" the
// commit's suite may be served from go's cache and the merge's never is; with
// the key off both are the argv they always were.
func TestGroupSuiteRunner_StageBySettingPicksTheFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		setting, gate, want string
		cached              bool
	}{
		{"", "precommit", "go test -count=1 -shuffle=on ./hub ./leaf1 ./leaf2", false},
		{"off", "precommit", "go test -count=1 -shuffle=on ./hub ./leaf1 ./leaf2", false},
		{"edit", "precommit", "go test -count=1 -shuffle=on ./hub ./leaf1 ./leaf2", false},
		{"commit", "precommit", "go test ./hub ./leaf1 ./leaf2", true},
		{"", "premerge", "go test -race -count=1 -shuffle=on ./hub ./leaf1 ./leaf2", false},
		{"commit", "premerge", "go test -race -count=1 -shuffle=on ./hub ./leaf1 ./leaf2", false},
	}
	for _, c := range cases {
		root := hubMergeRepo(t)
		if c.setting != "" {
			write(t, root, "aphrollo.toml", "[aphrollo]\ntest-cache = \""+c.setting+"\"\ntest-cache-impure = [\"./leaf2\"]\n")
		}
		groups := stagedRootGroups(root)
		if len(groups) != 1 {
			t.Fatalf("setup: %d root groups", len(groups))
		}
		base, ok := DetectRunner(groups[0].Root)
		if !ok {
			t.Fatal("setup: no runner")
		}
		got, ok := groupSuiteRunner(c.gate, root, groups[0], base)
		if !ok {
			t.Fatalf("%s/%q: the group owes no suite", c.gate, c.setting)
		}
		if line := cmdLine(got); line != c.want {
			t.Errorf("%s, test-cache %q: ran %q, want %q", c.gate, c.setting, line, c.want)
		}
		if got.Cached != c.cached {
			t.Errorf("%s, test-cache %q: Cached = %v, want %v", c.gate, c.setting, got.Cached, c.cached)
		}
		if c.cached && !slices.Equal(got.Impure, []string{"./leaf2"}) {
			t.Errorf("%s, test-cache %q: Impure = %v, want [./leaf2]", c.gate, c.setting, got.Impure)
		}
	}
}

// The merge gate through its entry point: whatever test-cache says, every
// suite it runs carries -count=1 -shuffle=on.
func TestMechanical_TestCacheNeverTouchesTheMergeFlags(t *testing.T) {
	t.Parallel()
	root := hubMergeRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\ntest-cache = \"commit\"\n")

	var seen []Runner
	if res := Mechanical(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{
		"go test -race -count=1 -shuffle=on ./hub",
		"go test -count=1 -shuffle=on ./leaf1 ./leaf2",
	}
	if got := ranLines(seen); !slices.Equal(got, want) {
		t.Fatalf("the merge ran %q, want %q", got, want)
	}
}
