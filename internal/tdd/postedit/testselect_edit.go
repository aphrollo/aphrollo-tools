package postedit

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Test selection at the edit stage (test-select in aphrollo.toml, suite
// testselect.go). With the key on, the post-edit suite of a Go package runs the
// tests the coverage store says cover the edited functions, and anything it
// cannot map or the store cannot vouch for runs the package whole, saying why.
// Nothing here builds coverage: the store is the one commit-time mutation keeps.

// testSelectOldSource is the file as HEAD has it, nil for a file HEAD does not
// have (or git cannot read, which reads as new and so as more functions to
// cover, never fewer). A seam for the test.
var testSelectOldSource = func(root, rel string) []byte {
	out := gitOut(root, "show", "HEAD:./"+rel)
	if out == "" {
		return nil
	}
	return []byte(out)
}

// testSelectQuery asks the coverage store which tests cover the functions. A
// seam for the test.
var testSelectQuery = CoveringTests

// withTestSelect is r, the post-edit `go test <pkg>` run of the edit to
// target, narrowed to the tests that cover it when test-select is "edit" and
// the edit can be mapped. r comes back with its argv as it was and the reason
// in Select when it cannot, and untouched when the key is off or r is not a Go
// test run. touched is every other file the same write changed: Go files of
// the same package are mapped with the target and their tests run together,
// and anything else leaves the package whole.
func withTestSelect(r Runner, root, target string, touched []string) Runner {
	if editSelectMode(root) != "edit" || !isGoTestInvocation(r.Cmd, r.Args) {
		return r
	}
	whole := func(reason string, total int) Runner {
		r.Select = &Selection{Total: total, Reason: reason}
		return r
	}
	if len(r.Args) != 2 || !strings.HasPrefix(r.Args[1], "./") {
		if len(touched) > 0 {
			return whole("the write changed several files", 0)
		}
		return whole("the run is not one go package", 0)
	}
	pkg := strings.TrimPrefix(r.Args[1], "./")
	var (
		funcs, tests, shown []string
		prodEdited          bool
	)
	for _, file := range append([]string{target}, touched...) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return whole("the edited file is outside the project", 0)
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") || path.Dir(rel) != pkg {
			if file != target {
				return whole("the write changed several files", 0)
			}
			return whole("the edited file is not a go file of the package", 0)
		}
		src, err := os.ReadFile(file)
		if err != nil {
			return whole("the edited file could not be read", 0)
		}
		isTest := strings.HasSuffix(rel, "_test.go")
		diff := DiffFuncs(testSelectOldSource(root, rel), src, isTest)
		switch {
		case diff.Unmappable != "" && isTest:
			// The package's count still says how much of it runs whole.
			return whole(diff.Unmappable, testSelectQuery(root, pkg, nil).Total)
		case diff.Unmappable != "":
			return whole(diff.Unmappable, 0)
		case isTest:
			// An edit to a test file runs the file's tests: the production code
			// did not change with it, so no coverage is asked for it.
			tests = append(tests, diff.Tests...)
			shown = append(shown, path.Base(rel))
		default:
			prodEdited = true
			funcs = append(funcs, diff.Funcs...)
			shown = append(shown, diff.Funcs...)
		}
	}
	if !prodEdited {
		return withSelectedTests(r, tests, testSelectQuery(root, pkg, nil).Total, shown)
	}
	slices.Sort(funcs)
	q := testSelectQuery(root, pkg, slices.Compact(funcs))
	if !q.Fresh {
		return whole(q.Reason, q.Total)
	}
	return withSelectedTests(r, append(tests, q.Tests...), q.Total, shown)
}
