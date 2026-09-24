package suite

import (
	"sort"
	"strings"
)

// A source edit under src/ narrows to the crate's `--lib` target (issue #820):
// fast, and the right first signal, but the crate's integration test
// binaries — tests/*.rs and every declared [[test]] — are separate targets
// that run never builds, and they are very often where the code under edit is
// actually exercised. The edit hook keeps the scoped run and names what it
// left out, from cargo metadata, so its green never reads as the crate's.

// cargoIntegrationTargetsNotRun names, as `--test <name>`, every integration
// test target of the one package a `--lib` cargo run is scoped to that the
// run did not also select by name. Nil for anything else: a run with no
// `--lib` selects either the whole package or a target the caller chose, a
// `--tests` run builds every test target, and a run over zero or several
// packages has no single package to read. ws is the run's own directory, the
// workspace root cargoRunnerAt executes from; root stands in when it has none.
func cargoIntegrationTargetsNotRun(r Runner, root string) []string {
	if r.Cmd != "cargo" {
		return nil
	}
	pkg, lib, ran, ok := cargoLibRunSelection(r.Args)
	if !ok || !lib {
		return nil
	}
	ws := r.Dir
	if ws == "" {
		ws = cargoWorkspaceRoot(root)
	}
	var names []string
	for name := range cargoTestTargetsFor(ws)[pkg] {
		if !ran[name] {
			names = append(names, "--test "+name)
		}
	}
	sort.Strings(names)
	return names
}

// cargoLibRunSelection reads a cargo run's argv for the package it is scoped
// to, whether it selects the lib target, and the `--test` targets it names.
// ok is false when the run names no package or more than one, or selects
// every test target with `--tests`.
func cargoLibRunSelection(args []string) (pkg string, lib bool, ran map[string]bool, ok bool) {
	ran = map[string]bool{}
	var pkgs []string
	// A flag's separate value is consumed by setting skip rather than by
	// stepping the index inside the loop: an in-loop step is a mutation site
	// whose decrement never terminates, which a mutation run can only report
	// as a timeout and never as a caught mutant.
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		name, value, inline := strings.Cut(a, "=")
		if !inline && (name == "-p" || name == "--package" || name == "--test") && i+1 < len(args) {
			value, skip = args[i+1], true
		}
		switch name {
		case "-p", "--package":
			pkgs = append(pkgs, value)
		case "--test":
			ran[value] = true
		case "--lib":
			lib = true
		case "--tests":
			return "", false, nil, false
		}
	}
	if len(pkgs) != 1 {
		return "", false, nil, false
	}
	return pkgs[0], lib, ran, true
}
