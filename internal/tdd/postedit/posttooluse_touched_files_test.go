package postedit

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestFileArgPos_NamesTheOneFileOfEachNarrowedShape(t *testing.T) {
	cases := []struct {
		name string
		r    Runner
		want int
	}{
		{"go test pkg", Runner{Cmd: "go", Args: []string{"test", "./a"}}, 1},
		{"go test with a flag", Runner{Cmd: "go", Args: []string{"test", "-race", "./a"}}, -1},
		{"go build pkg", Runner{Cmd: "go", Args: []string{"build", "./a"}}, -1},
		{"go test alone", Runner{Cmd: "go", Args: []string{"test"}}, -1},
		{"pytest file", Runner{Cmd: "pytest", Args: []string{"-q", "t.py"}}, 1},
		{"pytest with a flag first", Runner{Cmd: "pytest", Args: []string{"-x", "t.py"}}, -1},
		{"pytest of three", Runner{Cmd: "pytest", Args: []string{"-q", "a.py", "b.py"}}, -1},
		{"vitest related", Runner{Cmd: "npx", Args: []string{"vitest", "related", "a.ts", "--run"}}, 2},
		{"vitest related without the run flag", Runner{Cmd: "npx", Args: []string{"vitest", "related", "a.ts", "--x"}}, -1},
		{"vitest run", Runner{Cmd: "npx", Args: []string{"vitest", "run"}}, -1},
		{"vitest of two", Runner{Cmd: "npx", Args: []string{"vitest", "related", "a.ts", "b.ts", "--run"}}, -1},
		{"jest related", Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "a.ts"}}, 2},
		{"jest of two", Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "a.ts", "b.ts"}}, -1},
		{"jest alone", Runner{Cmd: "npx", Args: []string{"jest", "a.ts"}}, -1},
		{"cargo", Runner{Cmd: "cargo", Args: []string{"test", "--lib"}}, -1},
	}
	for _, c := range cases {
		if got := fileArgPos(c.r); got != c.want {
			t.Errorf("%s: fileArgPos(%v) = %d, want %d", c.name, c.r, got, c.want)
		}
	}
}

// widenFixture is a directory with the files each case narrows, and the
// narrowing of the first of them.
func widenFixture(t *testing.T, base Runner, files ...string) (root string, touched []string, first Runner) {
	t.Helper()
	root = mkProject(t)
	for _, f := range files {
		write(t, root, f, "x\n")
		touched = append(touched, filepath.Join(root, filepath.FromSlash(f)))
	}
	return root, touched, NarrowToRelatedTests(base, touched[0], root)
}

func TestWithTouchedFiles_NamesEveryPackageInTheOrderTheFilesCame(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	root, touched, first := widenFixture(t, base, "a/a.go", "b/b.go", "c/c.go")

	got := withTouchedFiles(first, base, root, touched)

	if want := []string{"test", "./a", "./b", "./c"}; !slices.Equal(got.Args, want) {
		t.Fatalf("args = %v, want %v", got.Args, want)
	}
}

// A widened line names at most maxWidenedTargets packages: one more than that
// is the root's broad run.
func TestWithTouchedFiles_TheFortyFirstPackageIsTheBroadRun(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	var files []string
	for i := range maxWidenedTargets + 1 {
		files = append(files, fmt.Sprintf("p%02d/x.go", i))
	}
	root, touched, first := widenFixture(t, base, files...)

	atBound := withTouchedFiles(first, base, root, touched[:maxWidenedTargets])
	if got := len(atBound.Args) - 1; got != maxWidenedTargets || atBound.Args[maxWidenedTargets] != "./p39" {
		t.Errorf("at the bound: %d targets ending %q, want %d ending ./p39", got, atBound.Args[len(atBound.Args)-1], maxWidenedTargets)
	}
	past := withTouchedFiles(first, base, root, touched)
	if !slices.Equal(past.Args, base.Args) {
		t.Errorf("past the bound: args = %v, want the broad run %v", past.Args, base.Args)
	}
}

func TestWithTouchedFiles_AFileInAPackageAlreadyNamedAddsNothing(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	root, touched, first := widenFixture(t, base, "a/a.go", "a/a2.go", "b/b.go", "b/b2.go")

	got := withTouchedFiles(first, base, root, touched)

	if want := []string{"test", "./a", "./b"}; !slices.Equal(got.Args, want) {
		t.Fatalf("args = %v, want %v", got.Args, want)
	}
}

func TestWithTouchedFiles_AloneOrWithNothingMoreItChangesNothing(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	root, touched, first := widenFixture(t, base, "a/a.go")

	if got := withTouchedFiles(first, base, root, touched); !slices.Equal(got.Args, first.Args) {
		t.Errorf("one file: args = %v, want %v", got.Args, first.Args)
	}
	if got := withTouchedFiles(first, base, root, nil); !slices.Equal(got.Args, first.Args) {
		t.Errorf("no touched files: args = %v, want %v", got.Args, first.Args)
	}
}

// The argument the widening clones is not the one the caller's runner holds.
func TestWithTouchedFiles_LeavesTheRunnerItWasGivenAlone(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "./..."}}
	root, touched, first := widenFixture(t, base, "a/a.go", "b/b.go")

	withTouchedFiles(first, base, root, touched)

	if want := []string{"test", "./a"}; !slices.Equal(first.Args, want) {
		t.Fatalf("the given runner became %v, want %v", first.Args, want)
	}
}

// A file that narrows to something other than the same shape of run is only
// covered by the root's broad run.
func TestWithTouchedFiles_AFileThatNarrowsToAnotherShapeFallsBackToTheBroadRun(t *testing.T) {
	base := Runner{Cmd: "go", Args: []string{"test", "-race", "./..."}}
	root, touched, first := widenFixture(t, base, "a/a.go", "README.md")

	got := withTouchedFiles(first, base, root, touched)

	if !slices.Equal(got.Args, base.Args) {
		t.Fatalf("args = %v, want the broad run %v", got.Args, base.Args)
	}
}

func TestWithTouchedFiles_OtherToolsKeepTheirFixedPrefixAndSuffix(t *testing.T) {
	vitest := Runner{Cmd: "npx", Args: []string{"vitest", "run"}}
	root, touched, first := widenFixture(t, vitest, "src/a.ts", "src/b.ts", "src/c.ts")
	if got, want := withTouchedFiles(first, vitest, root, touched).Args, []string{"vitest", "related", "src/a.ts", "src/b.ts", "src/c.ts", "--run"}; !slices.Equal(got, want) {
		t.Errorf("vitest args = %v, want %v", got, want)
	}

	jest := Runner{Cmd: "npx", Args: []string{"jest"}}
	root, touched, first = widenFixture(t, jest, "src/a.ts", "src/b.ts")
	if got, want := withTouchedFiles(first, jest, root, touched).Args, []string{"jest", "--findRelatedTests", "src/a.ts", "src/b.ts"}; !slices.Equal(got, want) {
		t.Errorf("jest args = %v, want %v", got, want)
	}

	pytest := Runner{Cmd: "pytest"}
	root, touched, first = widenFixture(t, pytest, "tests/test_a.py", "tests/test_b.py")
	if got, want := withTouchedFiles(first, pytest, root, touched).Args, []string{"-q", "tests/test_a.py", "tests/test_b.py"}; !slices.Equal(got, want) {
		t.Errorf("pytest args = %v, want %v", got, want)
	}
}

// A runner that is not one of the narrowed shapes (a cargo target, widened by
// withTouchedTestTargets) is returned as it is.
func TestWithTouchedFiles_ARunnerOfAnotherShapeIsNotWidened(t *testing.T) {
	r := Runner{Cmd: "cargo", Args: []string{"test", "--lib"}}
	root := mkProject(t)

	got := withTouchedFiles(r, r, root, []string{filepath.Join(root, "src", "a.rs")})

	if !slices.Equal(got.Args, r.Args) {
		t.Fatalf("args = %v, want %v", got.Args, r.Args)
	}
}
