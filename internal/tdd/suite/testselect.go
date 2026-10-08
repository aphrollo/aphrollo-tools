package suite

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// An edit to one function used to rerun every test of its package at the edit
// stage. A repo that keeps a coverage store (the commit-time mutation flow
// builds one) can opt in to running only the tests that cover the edit, with
// `test-select` under [aphrollo]:
//
//	"off"  (the default) the post-edit suite runs the package whole
//	"edit" the post-edit suite runs the covering tests when the store can say
//
// Only the post-edit suite is ever narrowed. The precommit fail-first, the
// merge, CI and mutation keep the whole suite: a selected green is a green of
// those tests, and the gate line says so (selected M of K tests).
const testSelectKey = "test-select"

// editSelectMode is root's test-select setting, "off" for anything but "edit".
func editSelectMode(root string) string {
	setting, _ := tomlStringIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", testSelectKey)
	if strings.ToLower(strings.TrimSpace(setting)) == "edit" {
		return "edit"
	}
	return "off"
}

// selectArgMax is the longest -run pattern a selection names. A longer one is
// most of the package, and a line past it is not one every platform's command
// line holds (Windows allows about 32k characters in all).
const selectArgMax = 2000

// withSelectedTests is r, a `go test <pkg>` run, narrowed to the named tests
// that cover funcs. total is how many tests the package has. The pattern is one
// self-contained `-run=^(T1|T2)$` word after the package: names sorted and
// without repeats, each quoted for the regexp, anchored so a name that is the
// prefix of another selects only itself. A run it cannot narrow safely (not
// one package, the selection is the whole package or nothing, a pattern too
// long) comes back with its argv unchanged and the reason in Select.
func withSelectedTests(r Runner, tests []string, total int, funcs []string) Runner {
	whole := func(reason string) Runner {
		r.Select = &Selection{Total: total, Funcs: slices.Clone(funcs), Reason: reason}
		return r
	}
	if !isGoTestInvocation(r.Cmd, r.Args) || len(r.Args) != 2 || !strings.HasPrefix(r.Args[1], "./") || strings.Contains(r.Args[1], "...") {
		return whole("the run is not one go package")
	}
	names := slices.Clone(tests)
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) == 0 {
		return whole("no test was selected")
	}
	if len(names) >= total {
		return whole("the selection is the whole package")
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	pattern := "^(" + strings.Join(quoted, "|") + ")$"
	if len(pattern) > selectArgMax {
		return whole("the selection is too long for a command line")
	}
	r.Args = []string{r.Args[0], r.Args[1], "-run=" + pattern}
	r.Select = &Selection{Run: len(names), Total: total, Funcs: slices.Clone(funcs)}
	return r
}
