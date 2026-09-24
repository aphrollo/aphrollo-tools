package precommit

import (
	"path/filepath"
	"reflect"
	"testing"
)

// commitOwedSuites names the suite the commit gate owes for each staged root
// and the root its green is cached under. The commit-msg gate weighs a
// verification claim against exactly these, so a wrong answer here either
// lets a claim stand on a suite nobody ran or refuses one that did run.

// A Go root owes its staged package's suite under CI's flags, keyed at the
// module root.
func TestCommitOwedSuites_AGoRootOwesItsStagedPackageSuite(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	got := commitOwedSuites(root)

	want := []owedSuite{{Root: root, Runner: Runner{Cmd: "go", Args: []string{"test", "-count=1", "-shuffle=on", "./internal/x"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owed = %+v, want %+v", got, want)
	}
}

// A cargo root owes the touched crate's suite run from the workspace, keyed
// at the crate's own root, which is where the gate hashes and keys.
func TestCommitOwedSuites_ACargoCrateOwesItsSuiteAtTheCrateRoot(t *testing.T) {
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "crates/alpha/src/lib.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	got := commitOwedSuites(root)

	want := []owedSuite{{Root: filepath.Join(root, "crates", "alpha"), Runner: Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}, Dir: root}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owed = %+v, want %+v", got, want)
	}
}
