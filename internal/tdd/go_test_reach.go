package tdd

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Which packages' TESTS can reach a given package — the question a mutation
// proof has to answer before it may call a mutant a survivor, because a test
// that never ran is not evidence that nothing kills it.
//
// goReverseDependents (go_reverse_scope.go) answers a NEARBY question and was
// measured before being reused here rather than trusted by its name. It does
// not answer this one, in three ways:
//
//   - Its edges come from goWorkspaceDeps, which reads `go list {{.Deps}}`.
//     `.Deps` is the package's own transitive imports and EXCLUDES test
//     imports. Measured on a three-package module: a package whose
//     production code imports nothing and whose _test.go imports the mutated
//     package reports no dependencies at all, so it is not a dependent —
//     while its test is exactly the test that would kill the mutant.
//   - It applies goReverseScopeExclude, which deliberately drops
//     internal/workspace on cost grounds. A cost exclusion is legitimate for
//     the precommit scope it was written for; a selection that skips a
//     package by policy cannot ground a claim that nothing kills the mutant.
//   - It returns nil BOTH when the graph cannot be read and when nothing
//     depends on the package. For a scope widening those collapse safely (it
//     widens to nothing either way); for a verdict they are opposite answers
//     — "no test anywhere else can reach this" versus "I could not find out".
//
// So this is its own probe. What it shares with the widening it came from is
// the closure walk itself (dependentsOf, clippyscope.go), which does answer
// the reachability question once the edges include the test ones.

// goTestReachFn is the probe the proof path calls, a package var so a test
// can state a graph — or a failure to read one — without a real `go list`
// run. Mirrors goWorkspaceDepsFn and cargoWorkspaceDepsFn.
var goTestReachFn = goTestReachingPackages

// goListReachFormat asks one `go list` run for everything the reach graph
// needs: the package's import path, its directory, its transitive
// non-test dependencies, and the DIRECT imports of its two test variants
// (in-package _test.go and the external _test package). Tabs separate the
// fields because an import path never contains one.
const goListReachFormat = "{{.ImportPath}}\t{{.Dir}}\t" +
	"{{range .Deps}}{{.}} {{end}}\t{{range .TestImports}}{{.}} {{end}}\t{{range .XTestImports}}{{.}} {{end}}"

// goTestReachingPackages reports the package directories (relative to root,
// forward-slashed, "." for the root package) whose TEST BINARY can reach dir,
// dir itself included, sorted. The error is non-nil when the graph could not
// be read at all — no `go list`, a module that does not load — and it carries
// the child's own words, because a verdict that says only "inconclusive"
// leaves the reader with nothing to act on.
//
// Test imports are DIRECT edges while .Deps is already transitive; the
// closure walk over the union covers the rest, since a package whose test
// imports Q reaches everything Q's own .Deps reach.
func goTestReachingPackages(root, dir string) ([]string, error) {
	cmd := exec.Command("go", "list", "-f", goListReachFormat, "./...")
	// The answer is about root, not about wherever the gate was invoked —
	// the bug goPackageDirs still carries (it builds the import-path map
	// from the process's own working directory).
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	dirOf, imports := parseGoListReach(root, string(out))
	if len(dirOf) == 0 {
		return nil, fmt.Errorf("go list in %s named no package at all: %s", root, strings.TrimSpace(stderr.String()))
	}
	edges := map[string][]string{}
	for pkg, imps := range imports {
		var to []string
		for _, imp := range imps {
			if d, ok := dirOf[imp]; ok && d != pkg {
				to = append(to, d)
			}
		}
		if len(to) > 0 {
			edges[pkg] = dedupeSorted(to)
		}
	}
	reaching := []string{dir}
	reaching = append(reaching, dependentsOf(edges, []string{dir})...)
	return dedupeSorted(reaching), nil
}

// parseGoListReach splits the run's lines into the import-path -> directory
// map and each directory's outgoing edges (its own transitive deps plus its
// two test variants' direct imports), both keyed the way goPackageDir names a
// package: relative to root, forward slashes, "." for the root itself.
func parseGoListReach(root, out string) (dirOf map[string]string, imports map[string][]string) {
	dirOf = map[string]string{}
	imports = map[string][]string{}
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 5 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		dir := goReachDir(root, strings.TrimSpace(fields[1]))
		dirOf[strings.TrimSpace(fields[0])] = dir
		var imps []string
		for _, f := range fields[2:5] {
			imps = append(imps, strings.Fields(f)...)
		}
		imports[dir] = append(imports[dir], imps...)
	}
	return dirOf, imports
}

// goReachDir is relPackageDir in goPackageDir's dialect: the root package is
// "." here, not "", because that is what narrowSourceEdit's `go test ./<dir>`
// is built from.
func goReachDir(root, dir string) string {
	if rel := relPackageDir(root, dir); rel != "" {
		return rel
	}
	return "."
}
