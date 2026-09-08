package tdd

import (
	"strconv"
	"strings"
	"testing"
)

// The shard count is derived on the assumption that ONE shard is a bounded
// build: MutantsJobsCap takes min(cores/3, ramGB/8, 8), which budgets 8 GB of
// RAM for each shard. Nothing enforced that. Until the shards genuinely ran
// side by side only one tree copy ever fit on the disk, so they ran one at a
// time; now every shard's cargo defaults to the whole machine, and N shards
// wide open is N times the memory the shard count budgeted — which ends as an
// OOM or as swap thrash, losing every verdict the run had reached.
//
// Both terms are derived from the box at runtime and the smaller wins, which
// is the same shape the shard count's own derivation has. The memory term is
// the one that matters: it is the one the shard count claimed to enforce.
func TestMutantsBuildJobsCap_TakesTheSmallerOfTheCoreAndMemoryTerms(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                 string
		cores, ramGB, shards int
		jobs                 int
		why                  string
	}{
		// Cores to spare, memory the constraint: four shards of a 16 GB box
		// have 4 GB each, which is two 2 GB rustc jobs.
		{name: "many cores, little memory", cores: 64, ramGB: 16, shards: 4,
			jobs: 2, why: "min(cores 64/4=16, ram 16GB/4/2=2) — ram"},
		// Memory to spare, cores the constraint.
		{name: "much memory, few cores", cores: 8, ramGB: 256, shards: 4,
			jobs: 2, why: "min(cores 8/4=2, ram 256GB/4/2=32) — cores"},
		// More shards than cores: the floor, because cargo cannot build with
		// no jobs at all. Both terms reach zero here and neither is SMALLER,
		// so the cores term is named — a tie is not the memory term winning.
		{name: "more shards than cores", cores: 4, ramGB: 8, shards: 8,
			jobs: 1, why: "min(cores 4/8=0, ram 8GB/8/2=0) — cores"},
		// Memory that could not be READ is not memory that is absent: an
		// unknown reading does not constrain, exactly as in MutantsJobsCap.
		{name: "memory unknown", cores: 12, ramGB: 0, shards: 3,
			jobs: 4, why: "min(cores 12/3=4, ram unknown) — cores"},
		// The lone re-run: one shard, the whole box, which is what having it
		// to itself means.
		{name: "one shard has the box", cores: 12, ramGB: 64, shards: 1,
			jobs: 12, why: "min(cores 12/1=12, ram 64GB/1/2=32) — cores"},
	} {
		jobs, why := mutantsBuildJobsCap(c.cores, c.ramGB, c.shards)
		if jobs != c.jobs || why != c.why {
			t.Errorf("%s: mutantsBuildJobsCap(%d, %d, %d) = (%d, %q), want (%d, %q)",
				c.name, c.cores, c.ramGB, c.shards, jobs, why, c.jobs, c.why)
		}
	}
}

// The heuristic reads a box's shape; a repo whose shape it reads wrong says so
// itself, in the same TOML family as every other key the runner takes, and the
// number it declares is honoured verbatim.
func TestMutantsBuildJobs_RepoOverrideBeatsTheDerivedCap(t *testing.T) {
	// A box the derivation would hold to one job per shard.
	t.Cleanup(setMutantsBoxForTest(4, 4))

	derived, _ := mutantsBuildJobsForShards(MutantsConfig{}, 4)
	if derived != 1 {
		t.Fatalf("derived jobs = %d, want the floor on a box this small — the override has nothing to beat", derived)
	}

	jobs, why := mutantsBuildJobsForShards(MutantsConfig{BuildJobs: 9}, 4)
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
	t.Cleanup(setMutantsBoxForTest(24, 64))

	four := envValueOf(measureShardEnv(root, MutantsConfig{}, 1, 4), "CARGO_BUILD_JOBS")
	if four != "6" { // min(cores 24/4=6, ram 64GB/4/2=8)
		t.Errorf("CARGO_BUILD_JOBS = %q with four shards, want %q", four, "6")
	}
	two := envValueOf(measureShardEnv(root, MutantsConfig{}, 1, 2), "CARGO_BUILD_JOBS")
	if two != "12" { // min(cores 24/2=12, ram 64GB/2/2=16)
		t.Errorf("CARGO_BUILD_JOBS = %q with two shards, want %q — a run reduced to two shards must not "+
			"keep the width it would have used with four", two, "12")
	}
	if _, err := strconv.Atoi(two); err != nil {
		t.Errorf("CARGO_BUILD_JOBS = %q, want a number cargo can read", two)
	}
}
