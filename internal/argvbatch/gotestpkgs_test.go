package argvbatch

import (
	"slices"
	"testing"
)

// TestGoTestPackages_RebuildsARunOverASubsetKeepingEveryFlag pins the two
// halves a run splitter needs: the package list in the order given, and a
// builder that puts any subset of it back on the same line with the verb and
// the flags around it untouched.
func TestGoTestPackages_RebuildsARunOverASubsetKeepingEveryFlag(t *testing.T) {
	args := []string{"test", "-race", "-count=1", "-shuffle=on", "./cmd/x", "./internal/y", ".", "-timeout=9m"}

	pkgs, with := GoTestPackages("go", args)

	if want := []string{"./cmd/x", "./internal/y", "."}; !slices.Equal(pkgs, want) {
		t.Fatalf("packages = %v, want %v", pkgs, want)
	}
	got := with([]string{".", "./cmd/x"})
	want := []string{"test", "-race", "-count=1", "-shuffle=on", ".", "./cmd/x", "-timeout=9m"}
	if !slices.Equal(got, want) {
		t.Fatalf("run over a subset = %v, want %v", got, want)
	}
}

// TestGoTestPackages_AnswersNothingForALineItCannotRebuildSafely pins the
// refusals: a line whose other words might be a flag's value (`-run X`, `-o
// ./out`) could have a path-looking value mistaken for a package, and a
// command that is not a `go test` has no such list.
func TestGoTestPackages_AnswersNothingForALineItCannotRebuildSafely(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		args []string
	}{
		{"flag with a separate value", "go", []string{"test", "-run", "TestX", "./a", "./b"}},
		{"separate value after the list", "go", []string{"test", "./a", "./b", "-run", "TestX"}},
		{"output path looks like a package", "go", []string{"test", "-o", "./a.test", "./a", "./b"}},
		{"vet is not a test run", "go", []string{"vet", "./a", "./b"}},
		{"not go", "cargo", []string{"test", "./a", "./b"}},
		{"no package", "go", []string{"test", "-race"}},
	}
	for _, c := range cases {
		pkgs, with := GoTestPackages(c.cmd, c.args)
		if pkgs != nil || with != nil {
			t.Errorf("%s: GoTestPackages(%q, %v) = %v, want no list", c.name, c.cmd, c.args, pkgs)
		}
	}
}
