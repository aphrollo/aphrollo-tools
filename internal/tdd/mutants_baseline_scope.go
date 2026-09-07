package tdd

import "os"

// The commit gate has ALREADY proved the whole tree green for this tip — that
// is a precondition of the mutation job existing at all (see
// startMutantsJob). Re-proving the whole workspace inside the mutation run's
// OWN unmutated baseline buys nothing and widens the blast radius from the
// lane's own crates to every crate any lane owns: a wall-clock test in a
// crate the lane never touched, failing only because a foreign build shares
// the box, then vetoes a receipt that has nothing to do with it (issue #251).
//
// mutantsTouchedPackages narrows the baseline to exactly the cargo packages
// that own a file the lane's own scoped diff still carries a hunk for — the
// SAME file set RunMutantsJob already computed to build --in-diff (see
// scopeMutantsRun/moveAwareDiff), read back here rather than re-derived, and
// mapped onto package names the same way the commit gate's own touched-crates
// suite stage already does it (cargoPackagesOwning, via planCargoStages in
// precommit.go).
//
// cargo-mutants' baseline scenario already scopes itself to the packages
// owning the mutants under test (src/lab.rs's `run_baseline`, PackageSelection
// ::Explicit over the filtered mutant list — verified against the installed
// 27.1.0), so --package here is belt-and-braces: it forces that scope up
// front, at mutant DISCOVERY, rather than leaving it to fall out of whichever
// files --in-diff happened to keep. Passed straight through to `cargo
// test`/`cargo nextest run` as `--package <name>`, the flag is what makes a
// touched-crate's failing test still veto the run while an untouched crate's
// failing test cannot even execute to veto it.
//
// A touched-crate set this run cannot determine — the diff cannot be read
// back, or no touched file resolves to an owning package — returns nil, and
// mutantsProducerFlags then emits no --package flag at all: today's whole-workspace
// behaviour, never a silent "measure nothing".
func mutantsTouchedPackages(j MutantsJob) []string {
	data, err := os.ReadFile(j.Diff)
	if err != nil {
		return nil
	}
	files := diffFiles(string(data))
	if len(files) == 0 {
		return nil
	}
	repoRel := make([]string, 0, len(files))
	for f := range files {
		repoRel = append(repoRel, f)
	}
	ws := cargoWorkspaceRoot(j.RepoRoot)
	pkgs := cargoPackagesOwning(ws, toRootRelative(j.RepoRoot, ws, repoRel))
	if len(pkgs) == 0 {
		return nil
	}
	return pkgs
}
