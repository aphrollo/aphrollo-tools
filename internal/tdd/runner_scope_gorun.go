package tdd

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Go fail-first scoping: the proof runs the STAGED TESTS, not the packages
// that happen to own them. Package granularity is right for the mechanical
// stage (it re-runs a package's whole suite against the new source), and
// wrong for fail-first, whose entire question is whether these named tests go
// RED at HEAD — internal/tdd's own package suite is 400-600s to answer it for
// one test, which is what made the 600s fail-open reachable on every commit
// (#567).

// goTestFuncRe matches a top-level `func TestXxx(` declaration, the only shape
// `go test -run` can select. Benchmark/Fuzz/Example declarations are
// deliberately NOT matched: -run does not select them, so naming one would
// build a filter that matches nothing.
var goTestFuncRe = regexp.MustCompile(`(?m)^func\s+(Test[\p{L}\p{N}_]*)\s*\(`)

// goRunFilter is the `-run` value selecting exactly names and nothing else:
// anchored at both ends so TestFoo does not also pull in TestFooBar, and each
// name regexp-quoted so a name carrying metacharacters can neither widen the
// filter nor break the pattern. A staged `func TestFoo` with t.Run subtests
// still matches under `^(TestFoo)$` — go matches the filter against the
// top-level test name first, so its subtests come along.
func goRunFilter(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, regexp.QuoteMeta(n))
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// goTestFuncNames is the Test function names declared in the file at
// root/rel, in declaration order. An unreadable file reads as none, which the
// caller turns into the package-scoped fallback rather than a partial filter.
func goTestFuncNames(root, rel string) []string {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	var names []string
	for _, m := range goTestFuncRe.FindAllStringSubmatch(string(data), -1) {
		names = append(names, m[1])
	}
	return names
}

// narrowGoFailFirst scopes a Go fail-first runner to the packages owning the
// staged test files AND a -run filter naming the tests those files declare —
// `go test ./internal/x -run '^(TestA|TestB)$'`. root is the proof worktree
// (HEAD plus the staged test diff), so the names come from the version of the
// tests actually under judgment.
//
// It reports (r, false) — package-scoped fallback — the moment any staged file
// yields no name at all: a generated or fuzz-only file, an unreadable one, a
// file in a directory that is not a package. A filter that matches nothing
// would let the proof pass vacuously, which is worse than a slow proof.
//
// Reverse dependents are deliberately absent (unlike narrowToStaged): a
// dependent package declares none of these names, so running it under the
// filter would execute zero tests there and read as #317's vacuous run.
func narrowGoFailFirst(r Runner, root string, tests []string) (Runner, bool) {
	if r.Cmd != "go" || len(tests) == 0 {
		return r, false
	}
	seenPkg, seenName := map[string]bool{}, map[string]bool{}
	var pkgs, names []string
	for _, f := range tests {
		found := goTestFuncNames(root, f)
		if len(found) == 0 {
			return r, false
		}
		dir := goPackageDir(root, filepath.Dir(f))
		if !dirHasGoFiles(filepath.Join(root, dir)) {
			return r, false
		}
		pkg := "./" + dir
		if dir == "." {
			pkg = "."
		}
		if !seenPkg[pkg] {
			seenPkg[pkg] = true
			pkgs = append(pkgs, pkg)
		}
		for _, n := range found {
			if !seenName[n] {
				seenName[n] = true
				names = append(names, n)
			}
		}
	}
	sort.Strings(pkgs)
	sort.Strings(names)
	args := append([]string{"test"}, pkgs...)
	return Runner{Cmd: "go", Args: append(args, "-run", goRunFilter(names))}, true
}

// narrowNonCargoFailFirst is narrowFailFirstTests' non-cargo half: the Go
// test-name scoping above when it applies, else whatever related mode
// narrowToStaged offers, else the runner unchanged.
func narrowNonCargoFailFirst(r Runner, root string, tests []string) Runner {
	if scoped, ok := narrowGoFailFirst(r, root, tests); ok {
		return scoped
	}
	if scoped, narrowed := narrowToStaged(r, root, tests); narrowed {
		return scoped
	}
	return r
}
