package mutation

// What a gremlins job costs, which is not what a cargo-mutants shard costs.
//
// A Go lane was budgeted with the Cargo model and refused with 12 GB free:
// `one job needs 15.0 GB (5.8 MB source-tree copy + 15.0 GB build dir
// (estimated, never built))`. gremlins keeps no build dir of its own. It
// compiles through the shared GOCACHE, and what one worker puts on the drive
// under the run's temp area is its copy of the module (`wd-*`) plus the test
// binaries `go test` links for the package it is judging (`go-build*`), plus
// whatever those tests write to TMPDIR while they run.
//
// Measured on this repo's own tree, a two-worker run over internal/tdd, the
// largest test binary it links, sampled every 5 s for its whole mutant
// phase: a 6.1 MB copy and a 41-51 MB go-build dir per worker, gremlins' own
// directory peaking at 117 MB, and the whole measurement area at 174 MB with
// the tests' own scratch in it.

const (
	// mutantsGoJobBytes is the estimate — derived from that measured peak,
	// not measured on each run — for everything a gremlins worker writes
	// besides its source copy: test binaries and test scratch. Rounded well
	// up from the 87 MB per worker observed, for the same asymmetry the
	// Cargo figure keeps: admitting a job that cannot fit loses the run.
	mutantsGoJobBytes = 512 << 20

	// mutantsGoJobGB is what one gremlins worker is priced at in memory, the
	// Go counterpart of mutantsCargoShardGB. The two-worker run above peaked
	// at 645 MB of process tree, gremlins itself included, sampled every
	// 5 s; a coverage gather of the same tree, which runs every package's
	// tests at once, peaked at 649 MB sampled every second. 2 GB a worker is three times
	// the whole tree's peak, kept that high because a sample can miss a
	// compile's spike.
	mutantsGoJobGB = 2
)

// mutantsGoJobNeeds is what each of jobs gremlins workers needs: the tracked
// tree it copies, measured, and mutantsGoJobBytes for the rest. Every job is
// priced the same, because no job has a directory that outlives the run.
func mutantsGoJobNeeds(root string, jobs int) []shardNeed {
	copyBytes := mutantsCopyBytes(root)
	needs := make([]shardNeed, 0, jobs)
	for range jobs {
		needs = append(needs, shardNeed{copyBytes: copyBytes, targetBytes: mutantsGoJobBytes, goJob: true})
	}
	return needs
}

// mutantsUnitNeeds prices a run's units by the runner that will spend them,
// picked the same way MeasureLane picks the runner.
func mutantsUnitNeeds(root string, units int) []shardNeed {
	if isGoModuleRepo(root) {
		return mutantsGoJobNeeds(root, units)
	}
	return mutantsShardNeeds(root, units)
}
