package tdd

import "fmt"

// How wide the WHOLE RUN's cargo may build, and what each shard gets out of
// that budget.
//
// The first version of this file capped ONE shard — cores/shards and
// (ramGB/shards)/2 — which reads like a budget and bounds nothing: multiply
// it by the shard count and the run is back to as many rustc processes as the
// box has cores, each holding gigabytes. It failed exactly that way (issue
// #609). Seven shards on a 24-core, 63 GB box with a 16 GB pagefile got
// `min(cores 24/7=3, ram 63GB/7/2=4) — cores`: the memory term never bound
// anything, 21 cold rustc processes started together, and all seven baselines
// died — `The paging file is too small for this operation to complete. (os
// error 1455)`, then `0xc0000142`, then the corrupted metadata the killed
// processes left behind.
//
// So the budget is the run's: a TOTAL number of concurrent cargo jobs,
// divided between the shards. The memory term is priced per phase, because a
// cold job and a warm one do not cost the same thing at all, and it is stated
// out loud in the log with the total in it — a run that builds narrow has to
// be explainable from the log rather than from a code read.

const (
	// mutantsRAMGBPerWarmBuildJob and mutantsRAMGBPerColdBuildJob are how
	// much memory ONE rustc job is budgeted, warm and cold. Both are
	// ESTIMATES, not measurements, and both are derived from the run in issue
	// #609 rather than from a profiler: on that box a cold rustc over
	// bevy/wgpu/windows-class crates peaked at 3-6 GB, and a warm
	// re-compilation of a handful of changed crates stayed around 2 GB. No
	// single number is right for every crate; these err in the direction that
	// costs wall clock rather than the direction that costs the run.
	mutantsRAMGBPerWarmBuildJob = 2
	mutantsRAMGBPerColdBuildJob = 6
)

// mutantsBuildJobsKey is the repo's own override, in the same TOML family as
// the rest of the runner's keys. A heuristic that reads two numbers off a box
// will read some box wrong, and the repo that knows better says so rather
// than living with it.
const mutantsBuildJobsKey = "mutants-build-jobs"

// mutantsBuildJobsCap is how wide one shard may build, and why. The budget is
// the whole run's and both of its terms are derived from the box passed in —
// nothing here knows what a particular machine looks like:
//
//	total, by cores:  cores
//	total, by memory: ramGB / (cold ? mutantsRAMGBPerColdBuildJob : ...Warm...)
//	per shard:        total / shards
//
// Memory that could not be READ is not memory that is absent: an unknown
// reading does not constrain and the cores decide alone, the same rule
// MutantsJobsCap follows for the same reason. The floor is one job per shard,
// because a cargo asked for zero builds nothing — and when the floor is what
// decides, the shards together are wider than the budget allowed, so the
// report says so rather than presenting a number the box cannot keep.
func mutantsBuildJobsCap(cores, ramGB, shards int, cold bool) (int, string) {
	if shards < 1 {
		shards = 1
	}
	perJobGB, phase := mutantsRAMGBPerWarmBuildJob, "warm"
	if cold {
		perJobGB, phase = mutantsRAMGBPerColdBuildJob, "cold"
	}
	total, why := cores, "cores"
	ram := "ram unknown"
	if ramGB > 0 {
		byRAM := ramGB / perJobGB
		ram = fmt.Sprintf("ram %dGB/%dGB=%d", ramGB, perJobGB, byRAM)
		if byRAM < total {
			total, why = byRAM, "ram"
		}
	}
	jobs, floored := total/shards, ""
	if jobs < 1 {
		jobs, floored = 1, ", floored at 1 per shard"
	}
	return jobs, fmt.Sprintf("min(cores %d, %s) — %s: %d total across %d shard%s, %s%s",
		cores, ram, why, total, shards, plural(shards), phase, floored)
}

// mutantsBuildJobsForShards is the number a run actually uses: the repo's
// declared value when it declares one, and otherwise this box divided between
// the shards it is running with.
//
// shards is the FINAL count — after the disk budget has reduced it to what
// the drive fits. A cap derived from the number the box first proposed would
// give a run cut from seven shards to two two-sevenths of the parallelism it
// could safely use.
//
// cold is the run's own phase, not a per-shard fact: a run that has to build
// anything from scratch prices EVERY job it may start at the cold figure,
// because the cheap warm jobs are not the ones that exhaust the box.
func mutantsBuildJobsForShards(cfg MutantsConfig, shards int, cold bool) (int, string) {
	if cfg.BuildJobs > 0 {
		return cfg.BuildJobs, fmt.Sprintf("%s = %d", mutantsBuildJobsKey, cfg.BuildJobs)
	}
	cores, ramGB := mutantsBoxShapeFn()
	return mutantsBuildJobsCap(cores, ramGB, shards, cold)
}

// mutantsShardTargetIsCold reports whether this shard would build from
// scratch: its persistent target dir holds nothing. An EMPTY directory is
// cold, not warm — measureShardEnv creates it before the process starts, so a
// run that died in its first minute leaves one behind, the same reason the
// disk budget refuses to read an empty dir as a measurement.
func mutantsShardTargetIsCold(root string, shard int) bool {
	_, size := dirNewestAndSize(mutantsShardTargetDir(root, shard))
	return size == 0
}

// mutantsShardsAreCold reports whether ANY of the shards about to run would
// build from scratch. One cold shard among warm ones still costs what a cold
// shard costs, so the run is priced by its most expensive job.
func mutantsShardsAreCold(root string, shards int) bool {
	for i := range shards {
		if mutantsShardTargetIsCold(root, i) {
			return true
		}
	}
	return false
}
