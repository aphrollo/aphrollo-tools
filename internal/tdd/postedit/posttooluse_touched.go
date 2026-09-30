package postedit

import (
	"slices"
	"strings"
)

// Issue #922: one edit can change a crate's source and one of its
// integration test files together. The run narrows from the first file, so a
// src/ change selects the crate's `--lib` target, and the test file's own
// `--test <name>` target, the one holding the test the edit just wrote, used
// to be named NOT RUN beside a green. Every test target a file of the edit
// maps to is part of what the edit's run selects.

// withTouchedTestTargets widens a scoped cargo `--lib` run r with each
// integration test target that another file of the same edit (touched,
// absolute paths) narrows to, reading the target through the same mapping
// an edit to that file alone would use (NarrowToRelatedTests over base, the
// root's detected runner). Only a target r would otherwise report NOT RUN is
// added, so the widening applies exactly where that clause would have named
// a touched target. The lib's module filter is dropped on widening: cargo
// applies a name filter to every selected binary, and an integration test's
// names never carry the module path.
func withTouchedTestTargets(r, base Runner, root string, touched []string) Runner {
	notRun := cargoIntegrationTargetsNotRun(r, root)
	if len(notRun) == 0 {
		return r
	}
	var add []string
	for _, name := range notRun {
		for _, f := range touched {
			if selectsTarget(NarrowToRelatedTests(base, f, root), name) {
				add = append(add, name)
				break
			}
		}
	}
	if len(add) == 0 {
		return r
	}
	wide := Runner{Cmd: r.Cmd, Args: append([]string{}, r.Args...), Dir: r.Dir}
	if unfiltered, dropped := dropCargoNameFilter(r); dropped {
		wide = unfiltered
	}
	for _, name := range add {
		wide.Args = append(wide.Args, "--test", name[len("--test "):])
	}
	return wide
}

// fileArgPos is where the one file or package a narrowed run names sits in
// r's argv, -1 when r is not one of the narrowed shapes that name exactly one:
// `go test <pkg>`, `pytest -q <file>`, `npx vitest related <file> --run` and
// `npx jest --findRelatedTests <file>`.
func fileArgPos(r Runner) int {
	a := r.Args
	switch {
	case r.Cmd == "go" && len(a) == 2 && a[0] == "test":
		return 1
	case r.Cmd == "pytest" && len(a) == 2 && a[0] == "-q":
		return 1
	case r.Cmd == "npx" && len(a) == 4 && a[0] == "vitest" && a[1] == "related" && a[3] == "--run":
		return 2
	case r.Cmd == "npx" && len(a) == 3 && a[0] == "jest" && a[1] == "--findRelatedTests":
		return 2
	}
	return -1
}

// withTouchedFiles widens a run r narrowed to one file or package to name
// what every other file of the same edit (touched, absolute paths) narrows
// to, as an edit to each alone would run it: a Bash command that rewrites
// files in several packages owes each package's tests, not the first one's.
// A touched file that narrows to anything but the same shape of run means the
// run that covers it is the root's broad one, base.
func withTouchedFiles(r, base Runner, root string, touched []string) Runner {
	pos := fileArgPos(r)
	if pos < 0 {
		return r
	}
	args := slices.Clone(r.Args)
	added := 0
	for _, f := range touched {
		n := NarrowToRelatedTests(base, f, root)
		if n.Cmd != r.Cmd || fileArgPos(n) != pos || !slices.Equal(n.Args[:pos], r.Args[:pos]) || !slices.Equal(n.Args[pos+1:], r.Args[pos+1:]) {
			return base
		}
		if !slices.Contains(args, n.Args[pos]) {
			added++
			args = slices.Insert(args, pos+added, n.Args[pos])
		}
	}
	wide := r
	wide.Args = args
	return wide
}

// selectsTarget reports whether r's argv selects the integration target
// named "--test <name>" (the NOT RUN clause's own spelling). A cargo target
// name carries no space, so the pair is matched as whole words of the argv.
func selectsTarget(r Runner, name string) bool {
	return strings.Contains(" "+strings.Join(r.Args, " ")+" ", " "+name+" ")
}
