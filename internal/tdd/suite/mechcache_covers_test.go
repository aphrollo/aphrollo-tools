package suite

import "testing"

// mechCacheCovers answers "is this suite proven green at this state" for a
// reader that did not run it, so what it accepts is exactly what a claim may
// stand on. Each test records greens the way runSuiteStage and the edit hook
// do (mechCacheAdd of a mechKey) and asks about one owed suite.

// A filtered edit-time green proves the one test it selected. It never
// answers for the package suite the commit owes, even at the same state.
func TestMechCacheCovers_AFilteredGreenDoesNotAnswerForThePackageSuite(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	mechCacheAdd(mechKey(root, "h", Runner{Cmd: "go", Args: []string{"test", "-run", "^TestNext$", "./..."}}))

	owed := Runner{Cmd: "go", Args: []string{"test", "-count=1", "-shuffle=on", "./internal/x"}}
	if mechCacheCovers(root, "h", owed) {
		t.Fatal("a green filtered to one test answered for the package's whole suite")
	}
}

// The gate's owed argv carries CI's flags and the edit hook's does not, so
// the two never share a key. A green over the same packages with no filter
// is the same ground proven, and answers for it.
func TestMechCacheCovers_AnUnfilteredGreenOverThePackageAnswersUnderOtherFlags(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	mechCacheAdd(mechKey(root, "h", Runner{Cmd: "go", Args: []string{"test", "./..."}}))

	owed := Runner{Cmd: "go", Args: []string{"test", "-count=1", "-shuffle=on", "./internal/x"}}
	if !mechCacheCovers(root, "h", owed) {
		t.Fatal("a whole-module green at this state did not answer for one of its packages")
	}
}

// A green is a fact about the state it ran on. The same command green at
// another state says nothing about this one.
func TestMechCacheCovers_AGreenAtAnotherStateAnswersNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	full := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	mechCacheAdd(mechKey(root, "earlier", full))

	if mechCacheCovers(root, "now", full) {
		t.Fatal("a green recorded at another state answered for this one")
	}
}

// A runner the scope classifier cannot read (pytest, npm, zig) has no width
// to compare, so only its own exact key answers for it, and a readable
// whole-tree green beside it does not.
func TestMechCacheCovers_AnUnreadableRunnerIsAnsweredByItsExactKeyAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	pytest := Runner{Cmd: "pytest", Args: []string{"-q"}}
	mechCacheAdd(mechKey(root, "h", Runner{Cmd: "go", Args: []string{"test", "./..."}}))

	if mechCacheCovers(root, "h", pytest) {
		t.Fatal("a go green answered for a pytest suite")
	}
	mechCacheAdd(mechKey(root, "h", pytest))
	if !mechCacheCovers(root, "h", pytest) {
		t.Fatal("the pytest suite's own green at this state did not answer for it")
	}
}

// No state hash means git could not fingerprint the tree, and a tree nobody
// can name is one no green is about, whatever the cache holds.
func TestMechCacheCovers_NoStateHashProvesNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	full := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	mechCacheAdd(mechKey(root, "", full))

	if mechCacheCovers(root, "", full) {
		t.Fatal("an unhashable tree was answered by the cache")
	}
}
