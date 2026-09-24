package precommit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Issue #791, from borld's wheel-hysteresis lane: a `git merge main` was
// rejected and the rejection blamed a clippy warning in a crate the lane
// never touched. The real cause was an untracked work-in-progress test file
// in the lane's own tree that did not compile: the merge gate builds the
// worktree, so a file the merge does not carry decided the merge's verdict.
// A dirty source file inside what the gate builds is refused before any
// build, by name. One outside it (docs, another crate, scratch) cannot
// change the verdict and must not stop the merge.

// excludeLocally ignores rel through .git/info/exclude, so the fixture's
// ignore rule adds no untracked file of its own.
func excludeLocally(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goLaneMerge is a lane that added internal/a merged into a trunk that has
// internal/b: the merge builds ./internal/a and nothing else. dirty runs on
// trunk's checkout before the merge starts, as a session's own leftovers
// would.
func goLaneMerge(t *testing.T, dirty func(root string)) string {
	t.Helper()
	root, trunk := syncRepo(t,
		map[string]string{"internal/a/a.go": "package a\n\nfunc A() int { return 1 }\n"},
		map[string]string{"internal/b/b.go": "package b\n\nfunc B() int { return 1 }\n"})
	gitDo(t, root, "checkout", "-q", trunk)
	dirty(root)
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "lane/work")
	return root
}

func TestMechanical_RefusesAnUntrackedTestFileInsideABuiltGoPackage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := goLaneMerge(t, func(root string) {
		write(t, root, "internal/a/wip_test.go", "package a\n\nfunc TestWip(t *testing.T) { A( }\n")
	})

	res := Mechanical(root, refuseToRun(t))
	if !res.Blocked {
		t.Fatalf("an untracked test file inside the package the merge builds must refuse the merge before building; got %q", res.Message)
	}
	if !strings.Contains(res.Message, "internal/a/wip_test.go") {
		t.Fatalf("the refusal must name the dirty file; got %q", res.Message)
	}
}

func TestMechanical_AllowsAModifiedFileInAGoPackageTheMergeDoesNotBuild(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := goLaneMerge(t, func(root string) {
		write(t, root, "internal/b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	})

	if got, want := mechanicalRuns(t, root), []Runner{goTestRun("./internal/a")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a modified file outside the built packages must not stop the merge:\n got %+v\nwant %+v", got, want)
	}
}

func TestMechanical_AllowsAnIgnoredFileInsideABuiltGoPackage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := goLaneMerge(t, func(root string) {
		excludeLocally(t, root, "internal/a/scratch.go")
		write(t, root, "internal/a/scratch.go", "package a\n")
	})

	if got, want := mechanicalRuns(t, root), []Runner{goTestRun("./internal/a")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("an ignored file is not part of the tree the gate judges and must not stop the merge:\n got %+v\nwant %+v", got, want)
	}
}

// downstreamWorkspace stages a change to core_sim; lab depends on it and is
// built with it, aside is not.
func TestMechanical_RefusesAnUntrackedTestFileInsideABuiltCrate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})
	write(t, root, "crates/lab/tests/wip.rs", "#[test]\nfn wip() { lab::pin( }\n")

	res := Mechanical(root, refuseToRun(t))
	if !res.Blocked {
		t.Fatalf("an untracked test file in a crate the merge builds must refuse the merge before building; got %q", res.Message)
	}
	if !strings.Contains(res.Message, "crates/lab/tests/wip.rs") {
		t.Fatalf("the refusal must name the dirty file; got %q", res.Message)
	}
}

func TestMechanical_AllowsAModifiedFileInACrateTheMergeDoesNotBuild(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})
	write(t, root, "crates/aside/src/lib.rs", "pub fn other() -> i32 { 1 }\n")

	var ran []string
	if res := Mechanical(root, suiteRuns(&ran)); res.Blocked {
		t.Fatalf("a modified file in a crate the merge does not build must not stop the merge: %s", res.Message)
	}
	if len(ran) == 0 {
		t.Fatal("premise broken: the merge ran no suite")
	}
}

func TestMechanical_AllowsAnIgnoredFileInsideABuiltCrate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})
	excludeLocally(t, root, "crates/core/src/scratch.rs")
	write(t, root, "crates/core/src/scratch.rs", "fn scratch() {}\n")

	var ran []string
	if res := Mechanical(root, suiteRuns(&ran)); res.Blocked {
		t.Fatalf("an ignored file is not part of the tree the gate judges and must not stop the merge: %s", res.Message)
	}
	if len(ran) == 0 {
		t.Fatal("premise broken: the merge ran no suite")
	}
}

// A workspace manifest in the merge checks the workspace beyond the touched
// crates, so a crate nothing else would build is built this time.
func TestMechanical_RefusesADirtyFileInAnyCrateWhenTheWorkspaceManifestMoved(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/core\", \"crates/lab\", \"crates/aside\"]\nresolver = \"2\"\n\n[workspace.package]\nedition = \"2021\"\n")
	gitDo(t, root, "add", "Cargo.toml")
	write(t, root, "crates/aside/tests/wip.rs", "#[test]\nfn wip() { aside::other( }\n")

	res := Mechanical(root, refuseToRun(t))
	if !res.Blocked || !strings.Contains(res.Message, "crates/aside/tests/wip.rs") {
		t.Fatalf("a staged workspace manifest checks every crate, so a dirty file in aside must refuse the merge by name; got blocked=%v %q", res.Blocked, res.Message)
	}
}

func TestGoRunnerBuilds_CoversNamedPackagesAndPatternsOnly(t *testing.T) {
	for _, tc := range []struct {
		args []string
		dir  string
		want bool
	}{
		{[]string{"test", "-race", "./internal/a"}, "internal/a", true},
		{[]string{"test", "-race", "./internal/a"}, "internal/ab", false},
		{[]string{"test", "-race", "./internal/a"}, "internal/a/sub", false},
		{[]string{"test", "."}, ".", true},
		{[]string{"test", "."}, "internal/a", false},
		{[]string{"test", "./..."}, "internal/a", true},
		{[]string{"test", "./internal/..."}, "internal", true},
		{[]string{"test", "./internal/..."}, "internal/a/sub", true},
		{[]string{"test", "./internal/..."}, "internalx", false},
		{[]string{"test", "./internal/..."}, "cmd/x", false},
	} {
		if got := goRunnerBuilds(Runner{Cmd: "go", Args: tc.args}, tc.dir); got != tc.want {
			t.Errorf("goRunnerBuilds(%v, %q) = %v, want %v", tc.args, tc.dir, got, tc.want)
		}
	}
}

// A root's plan speaks only for files beneath it. A file outside the Go
// root reads as "../…" from it, which a `./...` run must not claim.
func TestRootPlanBuilds_NothingOutsideItsGoRoot(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "svc/go.mod", "module example.com/svc\n\ngo 1.26\n")
	write(t, repo, "svc/a.go", "package svc\n")
	write(t, repo, "other/x.go", "package other\n")
	plan := &rootPlan{goRunner: &Runner{Cmd: "go", Args: []string{"test", "./..."}}}
	svc := filepath.Join(repo, "svc")

	if !plan.builds(repo, svc, "svc/a.go") {
		t.Fatal("premise broken: a `./...` run builds its own root's package")
	}
	if plan.builds(repo, svc, "other/x.go") {
		t.Fatal("other/x.go lies outside the svc root; its `./...` run does not build it")
	}
}

// Likewise a cargo plan: a file outside the workspace walks up, from the
// workspace's point of view, to the workspace's own [package] manifest, and
// would read as owned by it.
func TestRootPlanBuilds_NothingOutsideItsCargoWorkspace(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "rust/Cargo.toml", "[package]\nname = \"solo\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, repo, "rust/src/lib.rs", "pub fn f() {}\n")
	write(t, repo, "tools/x.rs", "fn main() {}\n")
	ws := filepath.Join(repo, "rust")
	plan := &rootPlan{cargo: &cargoStagePlan{ws: ws, touched: []string{"solo"}, downstream: []string{"solo"}}, cargoOK: true}

	if !plan.builds(repo, ws, "rust/src/lib.rs") {
		t.Fatal("premise broken: the plan builds solo, which owns rust/src/lib.rs")
	}
	if plan.builds(repo, ws, "tools/x.rs") {
		t.Fatal("tools/x.rs lies outside the cargo workspace; no crate the plan builds owns it")
	}
}

// The dirty-tree check resolves each cargo root's plan before anything is
// built, and the stages reuse it: resolving it again would report every
// unowned staged file twice.
func TestMechanical_ReportsAnUnownedCargoFileOnceWhenThePlanIsResolvedUpFront(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := downstreamWorkspace(t)
	stubWorkspaceGraph(t, map[string][]string{"core_sim": nil, "lab": {"core_sim"}, "aside": nil})
	write(t, root, "scripts/tool.rs", "fn main() {}\n")
	gitDo(t, root, "add", "scripts/tool.rs")

	var ran []string
	out := captureStderr(t, func() {
		if res := Mechanical(root, suiteRuns(&ran)); res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
	})
	if n := strings.Count(out, "scripts/tool.rs has no owning cargo package"); n != 1 {
		t.Fatalf("the unowned file was reported %d times, want once:\n%s", n, out)
	}
}
