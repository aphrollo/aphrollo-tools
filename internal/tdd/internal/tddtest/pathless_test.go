package tddtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerdictWordTmp_PutsEveryTempDirUnderAPathCarryingTheVerdictWords: after
// the call, every directory the test takes from t.TempDir sits under a path
// holding "green" and "red", so a test using it runs from such a path on every
// box rather than on the one whose checkout happens to.
func TestVerdictWordTmp_PutsEveryTempDirUnderAPathCarryingTheVerdictWords(t *testing.T) {
	VerdictWordTmp(t)
	dir := t.TempDir()
	for _, word := range []string{"green", "red"} {
		if !strings.Contains(dir, word) {
			t.Errorf("t.TempDir() = %q, want a path containing %q", dir, word)
		}
	}
}

// TestVerdictWordTmp_HandsOutResolvedPaths: code under test that resolves
// symlinks prints a temp path in its resolved form. Where the temp root is a
// link, t.TempDir's own form would differ from it and escape Pathless, so the
// directories handed out are already resolved.
func TestVerdictWordTmp_HandsOutResolvedPaths(t *testing.T) {
	realRoot, err := os.MkdirTemp("", "tmp-real-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(realRoot) })
	link := realRoot + "-link"
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	t.Setenv("GOTMPDIR", link)

	VerdictWordTmp(t)
	dir := t.TempDir()

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dir != resolved {
		t.Fatalf("t.TempDir() = %q, want its resolved form %q", dir, resolved)
	}
}

// TestPathless_NeutralisesTheTestsTempTree: a verdict word inside the temp
// path is gone from the result, while the same word outside any path, and a
// path the test does not own, stay as they were.
func TestPathless_NeutralisesTheTestsTempTree(t *testing.T) {
	VerdictWordTmp(t)
	root := t.TempDir()
	cfg := t.TempDir()
	out := "gate: go test ./x in " + root + " → RED-MISSING-IMPL\nfull output: " + cfg + "/red.log" +
		"\nkept: /elsewhere/green\n"

	got := Pathless(t, out)

	want := "gate: go test ./x in <tmp>/002 → RED-MISSING-IMPL\nfull output: <tmp>/003/red.log\nkept: /elsewhere/green\n"
	if got != want {
		t.Fatalf("Pathless =\n%s\nwant\n%s", got, want)
	}
}

// fatalRecorder is a testing.TB whose Fatal records instead of stopping, so a
// test can observe the helper refusing.
type fatalRecorder struct {
	*testing.T
	fatal string
}

func (f *fatalRecorder) Fatal(args ...any) { f.fatal = fmt.Sprint(args...) }

// TestVerdictWordTmp_RefusesACallAfterTheFirstTempDir: t.TempDir fixes its
// parent at the first call, so a late VerdictWordTmp would change nothing and
// the test would run from an ordinary path believing otherwise. It refuses.
func TestVerdictWordTmp_RefusesACallAfterTheFirstTempDir(t *testing.T) {
	_ = t.TempDir()
	rec := &fatalRecorder{T: t}

	VerdictWordTmp(rec)

	if !strings.Contains(rec.fatal, "before the first t.TempDir") {
		t.Fatalf("a late call must refuse, got Fatal(%q)", rec.fatal)
	}
}
