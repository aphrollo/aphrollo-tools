package precommit

import (
	"fmt"
	"sort"
	"strings"
)

// untestedFilesShown is how many of a root's staged files the NOT RUN line
// names before it counts the rest.
const untestedFilesShown = 5

// untestedRootLines is one line per top-level directory of the staged files
// (repo-relative, slash-separated) that no runner covers, "./" for files at
// the repo top, sorted, each naming the files nothing was tested in.
func untestedRootLines(files []string) []string {
	byRoot := map[string][]string{}
	sorted := append([]string{}, files...)
	sort.Strings(sorted)
	for _, f := range sorted {
		root := "./"
		if dir, _, nested := strings.Cut(f, "/"); nested {
			root = dir + "/"
		}
		byRoot[root] = append(byRoot[root], f)
	}
	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	lines := make([]string, 0, len(roots))
	for _, root := range roots {
		names := byRoot[root]
		more := ""
		if len(names) > untestedFilesShown {
			more = fmt.Sprintf(" and %d more", len(names)-untestedFilesShown)
			names = names[:untestedFilesShown]
		}
		lines = append(lines, fmt.Sprintf("  %s: %s%s", root, strings.Join(names, ", "), more))
	}
	return lines
}

// noRunnerNotRun is the line a root with staged code and no detected runner
// prints: NOT RUN, the reason, and each untested directory with its staged
// files. A repo whose top carries no marker (a Python backend/ beside a
// frontend, with no pyproject) would otherwise read as one skip of the whole
// tree.
func noRunnerNotRun(gateName, root string, files []string) string {
	return fmt.Sprintf("gate %s: %s → NOT RUN — no test runner is detected for %d staged file(s): no go.mod, Cargo.toml, package.json, pyproject.toml, pytest.ini or setup.py marks a root above them and no directory holds a pytest layout, so nothing was tested and this pass is not a green for them\n%s",
		gateName, root, len(files), strings.Join(untestedRootLines(files), "\n"))
}
