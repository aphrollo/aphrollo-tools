package tdd

import "fmt"

// How wide ONE shard's cargo may build while the other shards build beside
// it.
//
// The shards only became genuinely concurrent when the copy stopped carrying
// the workspace target dir: before that a single copy filled the drive and
// they ran one at a time. Nothing bounded their combined build, so N shards
// on a 24-core box meant up to N x 24 rustc processes — while the shard count
// itself comes from MutantsJobsCap's min(cores/3, ramGB/8, 8), a derivation
// that has ALREADY budgeted 8 GB of RAM for each shard. An unbounded shard
// makes that RAM term a fiction, and the way it fails is an OOM or swap
// thrash that takes every verdict the run had reached with it.
//
// So each shard is given its share of the box, on both dimensions, smaller
// term wins — the same shape the shard count's own derivation has, and logged
// the same way so a slow run can be explained from the log alone.

// mutantsRAMGBPerBuildJob is how much memory ONE rustc job is budgeted. It is
// an ESTIMATE, not a measurement: a rustc compiling a large crate peaks
// anywhere from a few hundred megabytes to several gigabytes depending on the
// crate and its codegen units, and no number here is right for all of them. 2
// GB is the figure that divides the 8 GB the shard count already budgets per
// shard into four jobs; it errs low, which costs wall clock, rather than high,
// which costs the run.
const mutantsRAMGBPerBuildJob = 2

// mutantsBuildJobsKey is the repo's own override, in the same TOML family as
// the rest of the runner's keys. A heuristic that reads two numbers off a box
// will read some box wrong, and the repo that knows better says so rather
// than living with it.
const mutantsBuildJobsKey = "mutants-build-jobs"

// mutantsBuildJobsCap is how wide one shard may build, and why. Both terms
// are derived from the box passed in — nothing here knows what a particular
// machine looks like:
//
//	by cores:  cores / shards
//	by memory: (ramGB / shards) / mutantsRAMGBPerBuildJob
//
// Memory that could not be READ is not memory that is absent: an unknown
// reading does not constrain and the cores decide alone, the same rule
// MutantsJobsCap follows for the same reason. The floor is one job, because
// a cargo asked for zero builds nothing.
func mutantsBuildJobsCap(cores, ramGB, shards int) (int, string) {
	if shards < 1 {
		shards = 1
	}
	byCores := cores / shards
	jobs, why := byCores, "cores"
	ram := "ram unknown"
	if ramGB > 0 {
		byRAM := (ramGB / shards) / mutantsRAMGBPerBuildJob
		ram = fmt.Sprintf("ram %dGB/%d/%d=%d", ramGB, shards, mutantsRAMGBPerBuildJob, byRAM)
		if byRAM < jobs {
			jobs, why = byRAM, "ram"
		}
	}
	if jobs < 1 {
		jobs = 1
	}
	return jobs, fmt.Sprintf("min(cores %d/%d=%d, %s) — %s", cores, shards, byCores, ram, why)
}

// mutantsBuildJobsForShards is the number a run actually uses: the repo's
// declared value when it declares one, and otherwise this box divided between
// the shards it is running with.
//
// shards is the FINAL count — after the disk budget has reduced it to what
// the drive fits. A cap derived from the number the box first proposed would
// give a run cut from seven shards to two two-sevenths of the parallelism it
// could safely use.
func mutantsBuildJobsForShards(cfg MutantsConfig, shards int) (int, string) {
	if cfg.BuildJobs > 0 {
		return cfg.BuildJobs, fmt.Sprintf("%s = %d", mutantsBuildJobsKey, cfg.BuildJobs)
	}
	cores, ramGB := mutantsBoxShapeFn()
	return mutantsBuildJobsCap(cores, ramGB, shards)
}
