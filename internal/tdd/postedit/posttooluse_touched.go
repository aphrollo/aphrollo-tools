package postedit

import "strings"

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

// selectsTarget reports whether r's argv selects the integration target
// named "--test <name>" (the NOT RUN clause's own spelling). A cargo target
// name carries no space, so the pair is matched as whole words of the argv.
func selectsTarget(r Runner, name string) bool {
	return strings.Contains(" "+strings.Join(r.Args, " ")+" ", " "+name+" ")
}
