package rootseam

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTable_NothingRegisteredIsAbsent(t *testing.T) {
	t.Parallel()
	var tab Table[int]
	if _, ok := tab.Get(t.TempDir()); ok {
		t.Fatal("an empty table answered")
	}
}

func TestTable_AnswersTheRootAndEverythingUnderIt(t *testing.T) {
	t.Parallel()
	var tab Table[string]
	root := t.TempDir()
	tab.Set(root, "v")
	for _, at := range []string{root, filepath.Join(root, "sub"), filepath.Join(root, "sub", "deep")} {
		if got, ok := tab.Get(at); !ok || got != "v" {
			t.Errorf("Get(%q) = %q, %v, want v", at, got, ok)
		}
	}
}

func TestTable_ASiblingSharingANamePrefixIsNotUnderTheRoot(t *testing.T) {
	t.Parallel()
	var tab Table[string]
	root := filepath.Join(t.TempDir(), "repo")
	tab.Set(root, "v")
	if got, ok := tab.Get(root + "2"); ok {
		t.Fatalf("a sibling %q got %q", root+"2", got)
	}
	if _, ok := tab.Get(filepath.Dir(root)); ok {
		t.Fatal("the parent of a registered root got its override")
	}
}

func TestTable_TheLongestRootWins(t *testing.T) {
	t.Parallel()
	var tab Table[string]
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	tab.Set(outer, "outer")
	tab.Set(inner, "inner")
	if got, _ := tab.Get(filepath.Join(inner, "f")); got != "inner" {
		t.Errorf("under inner = %q, want inner", got)
	}
	if got, _ := tab.Get(filepath.Join(outer, "g")); got != "outer" {
		t.Errorf("under outer = %q, want outer", got)
	}
}

func TestTable_RestoreReturnsToTheValueItReplaced(t *testing.T) {
	t.Parallel()
	var tab Table[string]
	root := t.TempDir()
	restoreFirst := tab.Set(root, "first")
	restoreSecond := tab.Set(root, "second")

	restoreSecond()
	if got, _ := tab.Get(root); got != "first" {
		t.Fatalf("after the inner restore = %q, want first", got)
	}
	restoreFirst()
	if _, ok := tab.Get(root); ok {
		t.Fatal("after the last restore the root still answers")
	}
}

func TestStderr_IsOsStderrUntilARootRegisters(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if got := Stderr(root); got != os.Stderr {
		t.Fatalf("Stderr(unregistered) = %v, want os.Stderr", got)
	}
	var buf bytes.Buffer
	restore := SetStderr(root, &buf)
	if _, err := Stderr(filepath.Join(root, "sub")).Write([]byte("line\n")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "line\n" {
		t.Fatalf("sink holds %q, want %q", buf.String(), "line\n")
	}
	restore()
	if got := Stderr(root); got != os.Stderr {
		t.Fatalf("after restore Stderr = %v, want os.Stderr", got)
	}
}
