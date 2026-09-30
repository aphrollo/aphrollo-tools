package precommit

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

func TestStderrFor_RoutesARootsLinesToItsSinkAndNoOtherRoots(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repo")
	var buf bytes.Buffer
	t.Cleanup(rootseam.SetStderr(root, &buf))

	if _, err := stderrFor(filepath.Join(root, "pkg")).Write([]byte("mine\n")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "mine\n" {
		t.Fatalf("the sink holds %q, want %q", got, "mine\n")
	}
	if w := stderrFor(root + "2"); w == nil {
		t.Fatal("a sibling root has no stderr at all")
	}
}

func TestNpmNodeFor_NamesNodeForItsRootAndNotASibling(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "app")
	t.Cleanup(setNpmNodeAt(root, func() (string, error) { return "/stub/node", nil }))

	if got, err := npmNodeFor(filepath.Join(root, "web")); err != nil || got != "/stub/node" {
		t.Errorf("under the root: %q, %v, want /stub/node", got, err)
	}
	if got, _ := npmNodeFor(root + "2"); got == "/stub/node" {
		t.Errorf("a sibling got the stated node: %q", got)
	}
}

func TestNpmNodeFor_AMissingNodeForItsRootIsTheRootsAnswer(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "app")
	t.Cleanup(setNpmNodeAt(root, func() (string, error) { return "", errors.New("no node here") }))

	if _, err := npmNodeFor(root); err == nil || !strings.Contains(err.Error(), "no node here") {
		t.Errorf("npmNodeFor(root) = %v, want the stated error", err)
	}
}

func TestLinterPresentFor_StatesPresenceForItsRootAndNotASibling(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repo")
	t.Cleanup(setLookLinterAt(root, func() bool { return true }))

	if !linterPresentFor(filepath.Join(root, "mod")) {
		t.Error("the root's own stated linter was not found under it")
	}
	// The run states the linter absent for every root that states nothing
	// (TestMain), so a sibling must read as absent.
	if linterPresentFor(root + "2") {
		t.Error("a sibling read the root's stated linter")
	}
}

func TestLinterVersionFor_StatesAVersionForItsRootAndNotASibling(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repo")
	t.Cleanup(setLinterVersionAt(root, func(string) string { return "9.9.9-stub" }))

	if got := linterVersionFor(filepath.Join(root, "mod")); got != "9.9.9-stub" {
		t.Errorf("under the root: %q, want 9.9.9-stub", got)
	}
	if got := linterVersionFor(root + "2"); got == "9.9.9-stub" {
		t.Errorf("a sibling read the root's stated version: %q", got)
	}
}
