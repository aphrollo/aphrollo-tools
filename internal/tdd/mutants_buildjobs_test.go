package tdd

import (
	"strconv"
	"strings"
	"testing"
)

// The shard count is derived on the assumption that the shards' builds are
// bounded: MutantsJobsCap takes min(cores/3, ramGB/8, 8). Nothing enforced
// that — every shard's cargo defaulted to the whole machine, and N shards
// wide open is N times the memory the shard count budgeted, which ends as an
// OOM or as swap thrash that takes every verdict the run had reached.
//
// The budget is the RUN's: a total number of concurrent cargo jobs, the
// smaller of the two terms the box offers, divided between the shards. Both
// terms are derived from the box at runtime, and the arithmetic is in the
// answer so a narrow run explains itself.
func TestMutantsBuildJobsCap_TakesTheSmallerOfTheCoreAndMemoryTerms(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                 string
		cores, ramGB, shards int
		jobs                 int
		why                  string
	}{
		// Cores to spare, memory the constraint: a 16 GB box carries eight
		// warm 2 GB rustc jobs in total, which is two for each of four shards.
		{name: "many cores, little memory", cores: 64, ramGB: 16, shards: 4,
			jobs: 2, why: "min(cores 64, ram 16GB/2GB=8) — ram: 8 total across 4 shards, warm"},
		// Memory to spare, cores the constraint.
		{name: "much memory, few cores", cores: 8, ramGB: 256, shards: 4,
			jobs: 2, why: "min(cores 8, ram 256GB/2GB=128) — cores: 8 total across 4 shards, warm"},
		// More shards than cores: the floor, because cargo cannot build with
		// no jobs at all — and the report says the floor was reached, since
		// the shards then run wider together than the budget allowed. Both
		// terms are 4 here and neither is SMALLER, so the cores term is named:
		// a tie is not the memory term winning.
		{name: "more shards than cores", cores: 4, ramGB: 8, shards: 8,
			jobs: 1, why: "min(cores 4, ram 8GB/2GB=4) — cores: 4 total across 8 shards, warm, floored at 1 per shard"},
		// Memory that could not be READ is not memory that is absent: an
		// unknown reading does not constrain, exactly as in MutantsJobsCap.
		{name: "memory unknown", cores: 12, ramGB: 0, shards: 3,
			jobs: 4, why: "min(cores 12, ram unknown) — cores: 12 total across 3 shards, warm"},
		// The lone re-run: one shard, the whole box, which is what having it
		// to itself means.
		{name: "one shard has the box", cores: 12, ramGB: 64, shards: 1,
			jobs: 12, why: "min(cores 12, ram 64GB/2GB=32) — cores: 12 total across 1 shard, warm"},
	} {
		// 0 free memory: unreadable, so every case here is the RAM term's
		// own — the arithmetic a box with no measurement available still
		// derives. The free reading has its own cases in
		// mutants_freemem_test.go.
		jobs, why := mutantsBuildJobsCap(c.cores, c.ramGB, 0, c.shards, false)
		if jobs != c.jobs || why != c.why {
			t.Errorf("%s: mutantsBuildJobsCap(%d, %d, %d) = (%d, %q), want (%d, %q)",
				c.name, c.cores, c.ramGB, c.shards, jobs, why, c.jobs, c.why)
		}
	}
}

// The budget is the whole RUN's, and a COLD job is priced at what a cold job
// really costs. Both halves of that sentence are the regression (issue #609).
//
// The failing run's own numbers: 24 cores, 63 GB, a pagefile pinned at 16 GB,
// seven shards. The per-shard cap answered `min(cores 24/7=3, ram 63GB/7/2=4)
// — cores` and the memory term never bound anything, so 21 rustc processes
// started cold builds together. A cold rustc on that repo's crates peaks at
// 3-6 GB, so the run asked for 63-126 GB against about 79 GB of total commit
// and every one of the seven baselines died — os error 1455, then
// 0xc0000142, then the corrupted metadata of the processes that were killed.
//
// So the memory term is priced per PHASE and has to be able to bind: seven
// cold shards get one job each, and the same box warm keeps the three it
// always had, because the working repo's 15-minute seven-shard run must not
// get slower.
func TestMutantsBuildJobsCap_PricesAColdJobAtWhatAColdJobCosts(t *testing.T) {
	t.Parallel()
	const cores, ramGB, shards = 24, 63, 7

	cold, coldWhy := mutantsBuildJobsCap(cores, ramGB, 0, shards, true)
	warm, warmWhy := mutantsBuildJobsCap(cores, ramGB, 0, shards, false)

	if want := "min(cores 24, ram 63GB/6GB=10) — ram: 10 total across 7 shards, cold"; cold != 1 || coldWhy != want {
		t.Errorf("cold = (%d, %q), want (1, %q)", cold, coldWhy, want)
	}
	if want := "min(cores 24, ram 63GB/2GB=31) — cores: 24 total across 7 shards, warm"; warm != 3 || warmWhy != want {
		t.Errorf("warm = (%d, %q), want (3, %q) — the working case must not get narrower", warm, warmWhy, want)
	}
	// Derived from the incident rather than from the formula: whatever the
	// arithmetic is, every cold job the run may start at once has to fit in
	// the memory the box actually has.
	if got := cold * shards * mutantsRAMGBPerColdBuildJob; got > ramGB {
		t.Errorf("%d shards x %d cold jobs x %d GB = %d GB on a %d GB box — the budget does not bound the run",
			shards, cold, mutantsRAMGBPerColdBuildJob, got, ramGB)
	}
}

// The heuristic reads a box's shape; a repo whose shape it reads wrong says so
// itself, in the same TOML family as every other key the runner takes, and the
// number it declares is honoured verbatim.
func TestMutantsBuildJobs_RepoOverrideBeatsTheDerivedCap(t *testing.T) {
	// A box the derivation would hold to one job per shard.
	t.Cleanup(setMutantsBoxForTest(4, 4, 0))

	derived, _ := mutantsBuildJobsForShards(MutantsConfig{}, 4, false)
	if derived != 1 {
		t.Fatalf("derived jobs = %d, want the floor on a box this small — the override has nothing to beat", derived)
	}

	jobs, why := mutantsBuildJobsForShards(MutantsConfig{BuildJobs: 9}, 4, false)
	if jobs != 9 {
		t.Errorf("jobs = %d, want the declared 9 honoured verbatim", jobs)
	}
	if !strings.Contains(why, mutantsBuildJobsKey) {
		t.Errorf("why = %q, want it to name %s, so a run nobody can explain from the box is explained by the repo",
			why, mutantsBuildJobsKey)
	}
}

// A repo declares it in the same table as the rest of the runner's keys, and
// a value that is not a build width is refused rather than half-obeyed.
func TestReadMutantsConfig_ReadsTheBuildJobsOverrideAndRefusesRubbish(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\n"+mutantsBuildJobsKey+" = 6\n")

	cfg, err := ReadMutantsConfig(root)
	if err != nil {
		t.Fatalf("ReadMutantsConfig: %v", err)
	}
	if cfg.BuildJobs != 6 {
		t.Errorf("BuildJobs = %d, want the declared 6", cfg.BuildJobs)
	}

	write(t, root, "aphrollo.toml", "[aphrollo]\n"+mutantsBuildJobsKey+" = plenty\n")
	if _, err := ReadMutantsConfig(root); err == nil || !strings.Contains(err.Error(), mutantsBuildJobsKey) {
		t.Errorf("error = %v, want a refusal naming %s: a build width nobody can parse is not a default",
			err, mutantsBuildJobsKey)
	}
}

// The cap has to reach the shard's own environment, computed against the
// shards it is ACTUALLY running with — the disk budget reduces that number
// before the environment is built, and a cap derived from the count before
// the reduction gives each shard a fraction of the box it could safely use.
func TestMeasureShardEnv_CarriesTheBuildJobCapForTheShardsItRunsWith(t *testing.T) {
	root := t.TempDir()
	// An operator's own value must not decide how wide a mutation build runs:
	// the shard owns this name the way it owns CARGO_TARGET_DIR.
	t.Setenv("CARGO_BUILD_JOBS", "64")
	t.Cleanup(setMutantsBoxForTest(24, 64, 0))

	// Derived by the RUN and handed to the shard, which is the only way the
	// free-memory term can be read once for a whole measurement.
	fourJobs, _ := mutantsBuildJobsForShards(MutantsConfig{}, 4, false)
	four := envValueOf(measureShardEnv(root, MutantsConfig{}, 1, fourJobs), "CARGO_BUILD_JOBS")
	if four != "6" { // min(cores 24, ram 64GB/2GB=32) = 24 total, 6 each
		t.Errorf("CARGO_BUILD_JOBS = %q with four shards, want %q", four, "6")
	}
	twoJobs, _ := mutantsBuildJobsForShards(MutantsConfig{}, 2, false)
	two := envValueOf(measureShardEnv(root, MutantsConfig{}, 1, twoJobs), "CARGO_BUILD_JOBS")
	if two != "12" { // min(cores 24, ram 64GB/2GB=32) = 24 total, 12 each
		t.Errorf("CARGO_BUILD_JOBS = %q with two shards, want %q — a run reduced to two shards must not "+
			"keep the width it would have used with four", two, "12")
	}
	if _, err := strconv.Atoi(two); err != nil {
		t.Errorf("CARGO_BUILD_JOBS = %q, want a number cargo can read", two)
	}
}
