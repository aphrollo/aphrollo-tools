package mutation

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// covqueryEdit rewrites the fixture's p.go with the replacement applied once.
func covqueryEdit(t *testing.T, root, from, to string) {
	t.Helper()
	path := filepath.Join(root, "internal", "p", "p.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), from) {
		t.Fatalf("fixture has no %q", from)
	}
	mustWrite(t, path, strings.Replace(string(src), from, to, 1))
}

// covqueryMeasured is a repository whose store holds the tests that cover
// the fixture's lines, f at 4, g at 8 and h at 12.
func covqueryMeasured(t *testing.T, lines ...int) string {
	t.Helper()
	root := covbuildRepo(t, &fakeToolchain{})
	covbuildAsk(t, root, 2, lines...)
	return root
}

func TestCoveringTests_AFreshStoreNamesTheTestsThatCoverTheEditedFunction(t *testing.T) {
	root := covqueryMeasured(t, 4, 8, 12)
	covqueryEdit(t, root, "return 2", "return 22")
	got := CoveringTests(root, "internal/p", []string{"g"})
	if !got.Fresh || got.Reason != "" {
		t.Fatalf("not fresh: %q", got.Reason)
	}
	if want := []string{"Test_B"}; !slices.Equal(got.Tests, want) {
		t.Fatalf("tests = %v, want %v", got.Tests, want)
	}
	if got.Total != 3 {
		t.Fatalf("total = %d, want 3", got.Total)
	}
}

func TestCoveringTests_ATestTheStoreDoesNotKnowJoinsTheSelection(t *testing.T) {
	root := covqueryMeasured(t, 4)
	covqueryEdit(t, root, "return 1", "return 11")
	got := CoveringTests(root, "internal/p", []string{"f"})
	if !got.Fresh {
		t.Fatalf("not fresh: %q", got.Reason)
	}
	if want := []string{"Test_A", "Test_B", "Test_C"}; !slices.Equal(got.Tests, want) {
		t.Fatalf("tests = %v, want %v (B and C have no measurement)", got.Tests, want)
	}
}

func TestCoveringTests_NotFreshWhenTheStoreCannotVouch(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T) string
		funcs  []string
		reason string
	}{
		{"no store", func(t *testing.T) string {
			return covbuildRepo(t, &fakeToolchain{})
		}, []string{"f"}, "no coverage store"},
		{"the build key differs", func(t *testing.T) string {
			root := covqueryMeasured(t, 4, 8, 12)
			f, err := os.OpenFile(filepath.Join(root, "go.mod"), os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if _, err := f.WriteString("\n// another module file\n"); err != nil {
				t.Fatal(err)
			}
			return root
		}, []string{"f"}, "build inputs differ"},
		{"another function an entry names changed", func(t *testing.T) string {
			root := covqueryMeasured(t, 4, 8, 12)
			covqueryEdit(t, root, "return 1", "return 11")
			covqueryEdit(t, root, "return 2", "return 22")
			return root
		}, []string{"g"}, "no longer holds: f"},
		{"the function has no entry", func(t *testing.T) string {
			root := covqueryMeasured(t, 4)
			covqueryEdit(t, root, "return 3", "return 33")
			return root
		}, []string{"h"}, "no test covers h"},
		{"a declaration that is not a function changed", func(t *testing.T) string {
			root := covqueryMeasured(t, 4, 8, 12)
			covqueryEdit(t, root, "func g()", "var extra = 1\n\nfunc g()")
			return root
		}, []string{"g"}, "declaration that is not a function"},
		{"the package cannot be read", func(t *testing.T) string {
			return covqueryMeasured(t, 4)
		}, []string{"f"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.setup(t)
			dir := "internal/p"
			if tc.name == "the package cannot be read" {
				dir = "internal/missing"
				tc.reason = "reading the sources"
			}
			got := CoveringTests(root, dir, tc.funcs)
			if got.Fresh || len(got.Tests) != 0 {
				t.Fatalf("fresh = %v, tests = %v, want neither", got.Fresh, got.Tests)
			}
			if !strings.Contains(got.Reason, tc.reason) {
				t.Fatalf("reason = %q, want it to contain %q", got.Reason, tc.reason)
			}
		})
	}
}

func TestCoveringTests_NeverBuildsCoverage(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	CoveringTests(root, "internal/p", []string{"f"})
	if len(tc.calls) != 0 {
		t.Fatalf("the query ran %v, want no command", tc.calls)
	}
	if entries, _ := filepath.Glob(filepath.Join(CoverCacheDir(root), "*.json")); len(entries) != 0 {
		t.Fatalf("the query wrote %v", entries)
	}
}

func TestCoveringTests_ATestAddedSinceTheMeasurementRuns(t *testing.T) {
	root := covqueryMeasured(t, 4, 8, 12)
	path := filepath.Join(root, "internal", "p", "p_test.go")
	mustWrite(t, path, covbuildTests+"\nfunc Test_D(t *testing.T) { _ = f() }\n")
	covqueryEdit(t, root, "return 1", "return 11")
	got := CoveringTests(root, "internal/p", []string{"f"})
	if !got.Fresh {
		t.Fatalf("not fresh: %q", got.Reason)
	}
	if want := []string{"Test_A", "Test_D"}; !slices.Equal(got.Tests, want) {
		t.Fatalf("tests = %v, want %v", got.Tests, want)
	}
	if got.Total != 4 {
		t.Fatalf("total = %d, want 4", got.Total)
	}
}

func TestCoveringTests_ATestEditedSinceTheMeasurementRuns(t *testing.T) {
	root := covqueryMeasured(t, 4, 8, 12)
	path := filepath.Join(root, "internal", "p", "p_test.go")
	mustWrite(t, path, strings.Replace(covbuildTests, "_ = h()", "_ = h() + 1", 1))
	covqueryEdit(t, root, "return 1", "return 11")
	got := CoveringTests(root, "internal/p", []string{"f"})
	if !got.Fresh {
		t.Fatalf("not fresh: %q", got.Reason)
	}
	if want := []string{"Test_A", "Test_C"}; !slices.Equal(got.Tests, want) {
		t.Fatalf("tests = %v, want %v", got.Tests, want)
	}
}

func TestCoveringTests_NotFreshWhenATestHelperChanged(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	path := filepath.Join(root, "internal", "p", "p_test.go")
	mustWrite(t, path, covbuildTests+"\nfunc helperX() int { return 1 }\n")
	covbuildAsk(t, root, 2, 4, 8, 12)
	mustWrite(t, path, covbuildTests+"\nfunc helperX() int { return 2 }\n")
	covqueryEdit(t, root, "return 1", "return 11")
	got := CoveringTests(root, "internal/p", []string{"f"})
	if got.Fresh || !strings.Contains(got.Reason, "test helper helperX changed") {
		t.Fatalf("fresh = %v, reason = %q", got.Fresh, got.Reason)
	}
}

func TestCoveringTests_NotFreshWhenTestMainChanged(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	path := filepath.Join(root, "internal", "p", "p_test.go")
	mustWrite(t, path, covbuildTests+"\nfunc TestMain(m *testing.M) { m.Run() }\n")
	covbuildAsk(t, root, 2, 4, 8, 12)
	mustWrite(t, path, covbuildTests+"\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n")
	covqueryEdit(t, root, "return 1", "return 11")
	got := CoveringTests(root, "internal/p", []string{"f"})
	if got.Fresh || !strings.Contains(got.Reason, "TestMain") {
		t.Fatalf("fresh = %v, reason = %q", got.Fresh, got.Reason)
	}
}

// A caller that names no function (an edit to a test file) still learns how
// many tests the package has; that count is set whenever the sources read,
// fresh or not.
func TestCoveringTests_TheTotalIsKnownWhenTheStoreIsNotFresh(t *testing.T) {
	root := covbuildRepo(t, &fakeToolchain{})
	for _, funcs := range [][]string{nil, {"f"}} {
		got := CoveringTests(root, "internal/p", funcs)
		if got.Fresh || got.Total != 3 {
			t.Errorf("funcs %v: fresh = %v, total = %d, want not fresh and 3", funcs, got.Fresh, got.Total)
		}
	}
}
