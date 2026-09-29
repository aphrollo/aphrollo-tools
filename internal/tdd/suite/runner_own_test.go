package suite

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// These are suite's own tests of the runner-selection and scoping helpers in
// runner.go, runner_scope.go and runner_scope_gorun.go that only other
// packages' gate tests reach today.

// twoRootRepo is a tree with two Go project roots side by side: a/ and b/,
// each with its own go.mod, plus a marker-less docs/ directory that resolves
// to the repo's own root.
func twoRootRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	write(t, repo, "go.mod", "module example.com/top\n\ngo 1.26\n")
	write(t, repo, "a/go.mod", "module example.com/a\n\ngo 1.26\n")
	write(t, repo, "a/a.go", "package a\n")
	write(t, repo, "a/a_test.go", "package a\n")
	write(t, repo, "b/go.mod", "module example.com/b\n\ngo 1.26\n")
	write(t, repo, "b/b.go", "package b\n")
	write(t, repo, "docs/readme.txt", "x\n")
	return repo
}

// TestStagedProjectRoots_GroupsFilesBySortedDistinctRoot pins the grouping:
// each file resolves to its nearest project root, the roots come back sorted
// and once each however many files share one.
func TestStagedProjectRoots_GroupsFilesBySortedDistinctRoot(t *testing.T) {
	t.Parallel()
	repo := twoRootRepo(t)
	got := stagedProjectRoots(repo, []string{"b/b.go", "a/a.go", "a/a_test.go", "docs/readme.txt"})
	want := []string{repo, filepath.Join(repo, "a"), filepath.Join(repo, "b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stagedProjectRoots = %v, want %v", got, want)
	}
}

// TestStagedProjectRoots_NothingStagedHasNoRoots pins the empty answer.
func TestStagedProjectRoots_NothingStagedHasNoRoots(t *testing.T) {
	t.Parallel()
	if got := stagedProjectRoots(twoRootRepo(t), nil); len(got) != 0 {
		t.Fatalf("stagedProjectRoots = %v, want none", got)
	}
}

// TestFilesUnderRoot_KeepsOnlyThatRootsFilesInInputOrder pins the filter.
func TestFilesUnderRoot_KeepsOnlyThatRootsFilesInInputOrder(t *testing.T) {
	t.Parallel()
	repo := twoRootRepo(t)
	got := filesUnderRoot(repo, filepath.Join(repo, "a"), []string{"b/b.go", "a/a_test.go", "docs/readme.txt", "a/a.go"})
	if want := []string{"a/a_test.go", "a/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filesUnderRoot = %v, want %v", got, want)
	}
}

// TestToRootRelative_RewritesPathsRelativeToTheRootForwardSlashed pins the
// rewrite: repo-relative in, root-relative out.
func TestToRootRelative_RewritesPathsRelativeToTheRootForwardSlashed(t *testing.T) {
	t.Parallel()
	repo := twoRootRepo(t)
	got := toRootRelative(repo, filepath.Join(repo, "a"), []string{"a/a.go", "a/sub/deep_test.go", "b/b.go"})
	want := []string{"a.go", "sub/deep_test.go", "../b/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toRootRelative = %v, want %v", got, want)
	}
}

// TestToRootRelative_APathThatCannotBeRelatedIsDropped pins the guard: with a
// relative root and an absolute repo the pair cannot be related, and the file
// is dropped rather than handed on as a bogus argument.
func TestToRootRelative_APathThatCannotBeRelatedIsDropped(t *testing.T) {
	t.Parallel()
	got := toRootRelative("/abs/repo", "relative/root", []string{"a.go"})
	if len(got) != 0 {
		t.Fatalf("toRootRelative = %v, want the unrelatable file dropped", got)
	}
}

// Serial: puts a fake cargo-nextest on the process-wide PATH.
// TestCargoVerbArgs_NextestNeedsBothItsConfigAndItsBinary pins the choice: the
// checked-in config alone is not enough, nor is the binary alone.
func TestCargoVerbArgs_NextestNeedsBothItsConfigAndItsBinary(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, ".config/nextest.toml", "[profile.default]\n")
	tddtest.PutFakeNextest(t)
	if got := cargoVerbArgs(ws); !reflect.DeepEqual(got, []string{"nextest", "run"}) {
		t.Fatalf("with config and binary: %v, want nextest run", got)
	}
	if got := cargoVerbArgs(t.TempDir()); !reflect.DeepEqual(got, []string{"test"}) {
		t.Fatalf("without a config: %v, want plain test", got)
	}
}

// TestCargoRunArgs_KeepsNextestAndDefaultsToTest pins the verb prefix.
func TestCargoRunArgs_KeepsNextestAndDefaultsToTest(t *testing.T) {
	t.Parallel()
	if got := cargoRunArgs(Runner{Args: []string{"nextest", "run", "-p", "a"}}); !reflect.DeepEqual(got, []string{"nextest", "run"}) {
		t.Errorf("nextest: %v", got)
	}
	if got := cargoRunArgs(Runner{Args: []string{"test", "-p", "a"}}); !reflect.DeepEqual(got, []string{"test"}) {
		t.Errorf("test: %v", got)
	}
	if got := cargoRunArgs(Runner{}); !reflect.DeepEqual(got, []string{"test"}) {
		t.Errorf("empty: %v", got)
	}
}

// TestCargoAphrolloKeys_ReadTheWorkspaceMetadataTable pins the manifest
// readers: the array keys come back sorted, the flag and string keys as
// declared, and an absent key or manifest reads as nothing.
func TestCargoAphrolloKeys_ReadTheWorkspaceMetadataTable(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\nmembers = []\n\n[workspace.metadata.aphrollo]\n"+
		"always-run = [\"zeta\", \"alpha\"]\nclippy-clean = [\"beta\"]\nstrict = true\nmode = \"fast\"\n")
	if got := cargoAlwaysRunPackages(ws); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Errorf("always-run = %v, want [alpha zeta]", got)
	}
	if got := cargoClippyCleanPackages(ws); !reflect.DeepEqual(got, []string{"beta"}) {
		t.Errorf("clippy-clean = %v, want [beta]", got)
	}
	if !cargoAphrolloFlag(ws, "strict") {
		t.Error("strict = true must read as set")
	}
	if got, ok := cargoAphrolloString(ws, "mode"); !ok || got != "fast" {
		t.Errorf("mode = (%q, %v), want (fast, true)", got, ok)
	}
	if cargoAphrolloFlag(ws, "absent") {
		t.Error("an absent flag is false")
	}
	if got := cargoAlwaysRunPackages(t.TempDir()); len(got) != 0 {
		t.Errorf("no manifest: %v, want none", got)
	}
}

// TestJSRunner_FallsBackToNpmTestWithoutAKnownRunner pins the last arm: no
// vitest or jest, or no readable package.json, is the generic script.
func TestJSRunner_FallsBackToNpmTestWithoutAKnownRunner(t *testing.T) {
	t.Parallel()
	want := Runner{Cmd: "npm", Args: []string{"test", "--silent"}}
	dir := t.TempDir()
	if got := jsRunner(dir); !reflect.DeepEqual(got, want) {
		t.Errorf("no package.json: %+v, want %+v", got, want)
	}
	write(t, dir, "package.json", `{"devDependencies":{"mocha":"1"}}`)
	if got := jsRunner(dir); !reflect.DeepEqual(got, want) {
		t.Errorf("unknown runner: %+v, want %+v", got, want)
	}
	write(t, dir, "package.json", `{ not json`)
	if got := jsRunner(dir); !reflect.DeepEqual(got, want) {
		t.Errorf("broken package.json: %+v, want %+v", got, want)
	}
}

// TestGoPackageDir_WalksUpToTheNearestDirectoryHoldingGoFiles pins the walk: an
// asset directory is not a package, so it resolves to the ancestor that is.
func TestGoPackageDir_WalksUpToTheNearestDirectoryHoldingGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "pkg/a.go", "package pkg\n")
	write(t, root, "pkg/testdata/fixtures/x.json", "{}")
	if got := goPackageDir(root, "pkg/testdata/fixtures"); got != "pkg" {
		t.Fatalf("goPackageDir = %q, want pkg", got)
	}
}

// TestGoPackageDir_NoGoFilesAnywhereAboveIsTheRoot pins the walk's end: with
// no Go files up the chain the answer is the module root, ".".
func TestGoPackageDir_NoGoFilesAnywhereAboveIsTheRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "assets/deep/x.bin", "x")
	if got := goPackageDir(root, "assets/deep"); got != "." {
		t.Fatalf("goPackageDir = %q, want .", got)
	}
}

// TestGoPackageDir_ADirectoryNotOnDiskKeepsTheLiteralMapping pins the deleted
// file case: nothing to judge by, so the directory asked for is returned.
func TestGoPackageDir_ADirectoryNotOnDiskKeepsTheLiteralMapping(t *testing.T) {
	t.Parallel()
	if got := goPackageDir(t.TempDir(), "gone/pkg"); got != "gone/pkg" {
		t.Fatalf("goPackageDir = %q, want gone/pkg", got)
	}
}

// TestNarrowGoSourceEdit_ANonGoFileInADirWithNoGoFilesLeavesTheRunnerBroad pins
// the #278 guard: naming a directory `go test` cannot load fails the run, so
// the broad runner covers the file instead.
func TestNarrowGoSourceEdit_ANonGoFileInADirWithNoGoFilesLeavesTheRunnerBroad(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "web/app.ts", "")
	broad := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	if got := narrowGoSourceEdit(broad, "web/app.ts", root); !reflect.DeepEqual(got, broad) {
		t.Fatalf("narrowGoSourceEdit = %+v, want the broad runner", got)
	}
	write(t, root, "web/glue.go", "package web\n")
	want := Runner{Cmd: "go", Args: []string{"test", "./web"}}
	if got := narrowGoSourceEdit(broad, "web/app.ts", root); !reflect.DeepEqual(got, want) {
		t.Fatalf("with a Go file beside it: %+v, want %+v", got, want)
	}
}

// TestDirHasGoFiles_OnlyRegularGoFilesCount pins the predicate: a directory
// named like a Go file, a non-Go file, and an unreadable directory are none.
func TestDirHasGoFiles_OnlyRegularGoFilesCount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "notes.txt", "x")
	write(t, dir, "weird.go/inner.txt", "x")
	if dirHasGoFiles(dir) {
		t.Error("a text file and a directory named weird.go are not Go files")
	}
	if dirHasGoFiles(filepath.Join(dir, "absent")) {
		t.Error("an unreadable directory holds none")
	}
	write(t, dir, "real.go", "package x\n")
	if !dirHasGoFiles(dir) {
		t.Error("a real .go file must count")
	}
}

// TestNarrowNonCargoFailFirst_UsesTheGoTestNamesWhenTheyResolve pins the first
// arm: Go tests with resolvable names scope to their package and names.
func TestNarrowNonCargoFailFirst_UsesTheGoTestNamesWhenTheyResolve(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {}\n")
	got := narrowNonCargoFailFirst(Runner{Cmd: "go", Args: []string{"test", "./..."}}, root, []string{"pkg/a_test.go"})
	want := Runner{Cmd: "go", Args: []string{"test", "./pkg", "-run", "^(TestOne)$"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowNonCargoFailFirst = %+v, want %+v", got, want)
	}
}

// TestNarrowNonCargoFailFirst_AnUnscopableRunnerComesBackUnchanged pins the
// last arm: a runner neither Go-name scoping nor staged narrowing can scope is
// returned as it came.
func TestNarrowNonCargoFailFirst_AnUnscopableRunnerComesBackUnchanged(t *testing.T) {
	t.Parallel()
	in := Runner{Cmd: "pytest"}
	if got := narrowNonCargoFailFirst(in, t.TempDir(), nil); !reflect.DeepEqual(got, in) {
		t.Fatalf("narrowNonCargoFailFirst = %+v, want the runner unchanged", got)
	}
}
